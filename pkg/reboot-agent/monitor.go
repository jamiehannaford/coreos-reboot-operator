package main

import (
	"github.com/jamiehannaford/coreos-reboot-operator/pkg/common"

	"github.com/coreos/locksmith/updateengine"
	"github.com/golang/glog"
)

func (a *rebootAgent) monitorSystem(ignoreCurrentState bool) {
	stop := make(chan struct{}, 1)
	ch := make(chan updateengine.Status, 1)

	ue, err := updateengine.New()
	if err != nil {
		glog.Fatalf("Error initializing update1 client: %v", err)
	}

	go ue.RebootNeededSignal(ch, stop)

	// First check status to make sure a reboot is already pending
	result, err := ue.GetStatus()
	if err != nil {
		glog.Fatalf("Cannot get update engine status: %v", err)
	}

	// Block current execution in order to wait for reboot signal
	if ignoreCurrentState || result.CurrentOperation != updateengine.UpdateStatusUpdatedNeedReboot {
		<-ch
	}

	close(stop)

	node, err := a.client.Core().Nodes().Get(a.nodeName)
	if err != nil {
		glog.Errorf("Could not get node %s", a.nodeName)
	}

	glog.Infof("Adding reboot-needed annotation")
	node.Annotations[common.RebootNeededAnnotation] = ""

	_, err = a.client.Core().Nodes().Update(node)
	if err != nil {
		glog.Errorf("Failed to update node: %v", err)
		return
	}

	glog.Infof("Node updated. Re-listening")
	a.monitorSystem(true)
}
