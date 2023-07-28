/*
Copyright 2023.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"context"
	"fmt"

	kapp "k8s.io/api/apps/v1"
	capp "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	canaryv1beta1 "maborosii.com/canary/api/v1beta1"
)

// CanaryRequestReconciler reconciles a CanaryRequest object
type CanaryRequestReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

//+kubebuilder:rbac:groups=canary.maborosii.com,resources=canaryrequests,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=canary.maborosii.com,resources=canaryrequests/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=canary.maborosii.com,resources=canaryrequests/finalizers,verbs=update
//+kubebuilder:rbac:groups=v1,resources=deployment,verbs=get;list;watch
//+kubebuilder:rbac:groups=v1,resources=pod,verbs=get;list;watch;create;update;patch;delete

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the CanaryRequest object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.15.0/pkg/reconcile
func (r *CanaryRequestReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := log.FromContext(ctx)
	var canaryRequest canaryv1beta1.CanaryRequest
	if err := r.Get(ctx, req.NamespacedName, &canaryRequest); err != nil {
		log.Error(err, "unable to fetch CanaryRequest")
		return ctrl.Result{}, nil

	}
	var deployment kapp.Deployment
	// search info for deployment
	ob := client.ObjectKey{Namespace: canaryRequest.Namespace, Name: canaryRequest.Spec.RefDeployment}
	// get deployment infomation
	if err := r.Get(ctx, ob, &deployment); err != nil {
		log.Error(err, "not found related deployment")
		return ctrl.Result{}, nil
	}
	var containerName = deployment.Name
	var imageName = canaryRequest.Spec.Image
	replicas, err := foundReplicasForDeployment(deployment)
	if err != nil {
		return ctrl.Result{}, err
	}
	log.Info("Deployment is healthy", "name: ", deployment.Name)

	podList, err := foundPodForDeployment(r, ctx, req, deployment)
	if err != nil {
		return ctrl.Result{}, err
	}
	for _, po := range podList.Items {
		log.Info("pod list info", "pod name: ", po.Name)
	}

	piList := foundImageForPodList(containerName, podList)
	var changePIList []*podImageInfo
	for _, pi := range piList {
		tmpPi := pi
		if !tmpPi.foundDestImage(imageName) {
			changePIList = append(changePIList, tmpPi)
		}
	}
	log.Info("length of not found dest image list", "", len(changePIList))

	changeNums := int(canaryRequest.Spec.Weight)
	if changeNums > int(replicas) {
		changeNums = int(replicas)
	}
	if changeNums <= int(replicas)-len(changePIList) {
		return ctrl.Result{}, nil
	}
	for i := 0; i < int(changeNums)+len(changePIList)-int(replicas); i++ {
		tmpPo := changePIList[i].Pod
		tmpPo.Spec.Containers[0].Image = imageName
		log.Info("change image for", "pod:", tmpPo.Name, "image:", imageName)
		// if err := r.Patch(ctx, tmpPo, client.MergeFrom(tmpPo)); err != nil {
		// 	log.Error(err, "patch pod image:")
		// 	return ctrl.Result{}, err
		// }
		if err := r.Update(ctx, tmpPo); err != nil {
			log.Error(err, "update pod image:")
			return ctrl.Result{}, err
		}
	}
	return ctrl.Result{}, nil
}

func foundReplicasForDeployment(dp kapp.Deployment) (int32, error) {
	if dp.Status.AvailableReplicas != dp.Status.Replicas {
		return 0, fmt.Errorf("%s's replicas is unhealthy", dp.Name)
	}
	return *dp.Spec.Replicas, nil
}

func foundPodForDeployment(r *CanaryRequestReconciler, ctx context.Context, req ctrl.Request, dp kapp.Deployment) (*capp.PodList, error) {
	var (
		podList capp.PodList
	)
	labels := dp.Spec.Selector.MatchLabels
	if err := r.List(ctx, &podList, client.InNamespace(req.Namespace), client.MatchingLabels(labels)); err != nil {
		return nil, fmt.Errorf("unable fetch pod list")
	}
	return &podList, nil
}

// type podImage map[string]string
type podImageInfo struct {
	*capp.Pod
	ImageName string
}
type Option func(*podImageInfo)

func newPodImageInfo(p *capp.Pod, options ...Option) *podImageInfo {
	mm := &podImageInfo{
		Pod: p,
	}
	for _, option := range options {
		option(mm)
	}
	return mm
}

func withImage(containerName string) Option {
	return func(pi *podImageInfo) {
		for _, c := range pi.Spec.Containers {
			if c.Name == containerName {
				pi.ImageName = c.Image
			}
		}
	}
}

func (p *podImageInfo) foundDestImage(image string) bool {
	if p.ImageName == image {
		return true
	}
	return false
}

// default setting: a pod only one container
func foundImageForPodList(containerName string, pl *capp.PodList) []*podImageInfo {
	var piList []*podImageInfo
	for _, po := range pl.Items {
		tmppo := po
		piList = append(piList, newPodImageInfo(&tmppo, withImage(containerName)))
	}
	return piList
}

// SetupWithManager sets up the controller with the Manager.
func (r *CanaryRequestReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&canaryv1beta1.CanaryRequest{}).
		Owns(&capp.Pod{}).
		Complete(r)
}
