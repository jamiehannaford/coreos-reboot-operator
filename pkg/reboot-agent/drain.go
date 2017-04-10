package main

import (
	"fmt"
	"time"

	"github.com/golang/glog"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/pkg/api/v1"
)

func (a *rebootAgent) cordonNode(node *v1.Node) {
	if node.Spec.Unschedulable == true {
		return
	}

	node.Spec.Unschedulable = true
}

func (a *rebootAgent) removePods(node *v1.Node) error {
	pods, err := a.getNodePods(node)
	if err != nil {
		return fmt.Errorf("Error retrieving pods for node %s: %v", node.Spec.ExternalID, err)
	}

	if len(pods) == 0 {
		return nil
	}

	// support eviction in the future

	err = a.deletePods(pods)
	if err != nil {
		return fmt.Errorf("Error deleting pods for node %s: %v", node.Spec.ExternalID, err)
	}

	return nil
}

func (a *rebootAgent) getNodePods(node *v1.Node) (pods []v1.Pod, err error) {
	// Move to metav1 "k8s.io/apimachinery/pkg/apis/meta/v1" when new client ships

	podList, err := a.client.Core().Pods("").List(v1.ListOptions{
		FieldSelector: fields.SelectorFromSet(fields.Set{"spec.nodeName": node.Name}).String()})

	// Need to add more complex filtering so that specific types of pods are not
	// unnecessarily deleted. For example: mirror pods, pods with local storage,
	// unreplicated pods, and DaemonSet pods. These filters should obey config
	// values set somewhere else in the stack, maybe in a TPR.

	return podList.Items, err
}

func (a *rebootAgent) deletePods(pods []v1.Pod) error {
	for _, pod := range pods {
		gracePeriodSeconds := int64(1)
		deleteOptions := &v1.DeleteOptions{GracePeriodSeconds: &gracePeriodSeconds}
		err := a.client.Core().Pods(pod.Namespace).Delete(pod.Name, deleteOptions)
		if err != nil {
			return err
		}
	}

	getPodFn := func(namespace, name string) (*v1.Pod, error) {
		return a.client.Core().Pods(namespace).Get(name)
	}

	return a.waitForPods(pods, getPodFn)
}

func (a *rebootAgent) waitForPods(pods []v1.Pod, getPodFn func(namespace, name string) (*v1.Pod, error)) error {
	interval := time.Duration(1)
	timeout := time.Duration(600)

	err := wait.PollImmediate(interval, timeout, func() (bool, error) {
		pendingPods := []v1.Pod{}
		for i, pod := range pods {
			p, err := getPodFn(pod.Namespace, pod.Name)
			if apierrors.IsNotFound(err) || (p != nil && p.ObjectMeta.UID != pod.ObjectMeta.UID) {
				glog.Infof("Pod %s has been deleted", pod.Name)
				continue
			} else if err != nil {
				return false, err
			} else {
				pendingPods = append(pendingPods, pods[i])
			}
		}
		pods = pendingPods
		if len(pendingPods) > 0 {
			return false, nil
		}
		return true, nil
	})

	return err
}
