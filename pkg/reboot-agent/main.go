package main

import (
	"flag"
	"os"
	"time"

	"github.com/coreos/go-systemd/login1"
	"github.com/golang/glog"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/pkg/api"
	"k8s.io/client-go/pkg/api/v1"
	"k8s.io/client-go/pkg/fields"
	"k8s.io/client-go/pkg/runtime"
	"k8s.io/client-go/pkg/util/wait"
	"k8s.io/client-go/pkg/watch"
	"k8s.io/client-go/tools/cache"

	"github.com/jamiehannaford/coreos-reboot-operator/pkg/common"
)

const nodeNameEnv = "NODE_NAME"

func main() {
	// When running as a pod in-cluster, a kubeconfig is not needed. Instead this will make use of the service account injected into the pod.
	// However, allow the use of a local kubeconfig as this can make local development & testing easier.
	kubeconfig := flag.String("kubeconfig", "", "Path to a kubeconfig file")

	// We log to stderr because glog will default to logging to a file.
	// By setting this debugging is easier via `kubectl logs`
	flag.Set("logtostderr", "true")
	flag.Parse()

	// The node name is necessary so we can identify "self".
	// This environment variable is assumed to be set via the pod downward-api, however it can be manually set during testing
	nodeName := os.Getenv(nodeNameEnv)
	if nodeName == "" {
		glog.Fatalf("Missing required environment variable %s", nodeNameEnv)
	}

	// Build the client config - optionally using a provided kubeconfig file.
	config, err := common.GetClientConfig(*kubeconfig)
	if err != nil {
		glog.Fatalf("Failed to load client config: %v", err)
	}

	// Construct the Kubernetes client
	client, err := kubernetes.NewForConfig(config)
	if err != nil {
		glog.Fatalf("Failed to create kubernetes client: %v", err)
	}

	// Open a dbus connection for triggering a system reboot
	dbusConn, err := login1.New()
	if err != nil {
		glog.Fatalf("Failed to create dbus connection: %v", err)
	}

	agent := newRebootAgent(nodeName, client, dbusConn)

	glog.Info("Starting Reboot Agent")

	go agent.controller.Run(wait.NeverStop)
	agent.monitorSystem(false)
}

type rebootAgent struct {
	client     kubernetes.Interface
	dbusConn   *login1.Conn
	controller cache.ControllerInterface
	nodeName   string
}

func newRebootAgent(nodeName string, client kubernetes.Interface, dbusConn *login1.Conn) *rebootAgent {
	agent := &rebootAgent{
		client:   client,
		dbusConn: dbusConn,
		nodeName: nodeName,
	}

	// We only care about updates to "self" so create a field selector based on the current node name
	nodeNameFS := fields.OneTermEqualSelector("metadata.name", nodeName)

	// We do not need the cache store of the informer. In this case we just want the controller event handlers.
	_, controller := cache.NewInformer(
		&cache.ListWatch{
			ListFunc: func(alo api.ListOptions) (runtime.Object, error) {
				var lo v1.ListOptions
				v1.Convert_api_ListOptions_To_v1_ListOptions(&alo, &lo, nil)

				// Add the field selector containgin our node name to our list options
				lo.FieldSelector = nodeNameFS.String()
				return client.Core().Nodes().List(lo)
			},
			WatchFunc: func(alo api.ListOptions) (watch.Interface, error) {
				var lo v1.ListOptions
				v1.Convert_api_ListOptions_To_v1_ListOptions(&alo, &lo, nil)

				// Add the field selector containgin our node name to our list options
				lo.FieldSelector = nodeNameFS.String()
				return client.Core().Nodes().Watch(lo)
			},
		},
		// The types of objects this informer will return
		&v1.Node{},
		// The resync period of this object. This will force a re-queue of all cached objects at this interval.
		// Every object will trigger the `Updatefunc` even if there have been no actual updates triggered.
		// In some cases you can set this to a very high interval - as you can assume you will see periodic updates in normal operation.
		// The interval is set low here for demo purposes.
		10*time.Second,
		// Callback Functions to trigger on add/update/delete
		cache.ResourceEventHandlerFuncs{
			// AddFunc: func(obj interface{}) {}
			UpdateFunc: agent.handleUpdate,
			// DeleteFunc: func(obj interface{}) {}
		},
	)

	agent.controller = controller

	return agent
}

func (a *rebootAgent) handleUpdate(oldObj, newObj interface{}) {
	// In an `UpdateFunc` handler, before doing any work, you might try and determine if there has ben an actual change between the oldObj and newObj.
	// This could mean checking the `resourceVersion` of the objects, and if they are the same - there has been no change to the object.
	// Or it might mean only inspecting fields that you care about (as seen below).
	// However, you should be careful when ignoring updates to objects, as it is possible that prior update was missed,
	// and if you continue to ignore the objects, you will never fully sync desired state.

	// Because we are about to make changes to the object - we make a copy.
	// You should never mutate the original objects (from the cache.Store) as you are modifying state that has not been persisted via the apiserver.
	// For example, if you modify the original object, but then your `Update()` call fails - your local cache could now be "wrong".
	// Additionally, if using SharedInformers - you are modifying a local cache that could be used by other controllers.
	node, err := common.CopyObjToNode(newObj)
	if err != nil {
		glog.Errorf("Failed to copy Node object: %v", err)
		return
	}

	glog.V(4).Infof("Received update for node: %s", node.Name)

	if shouldReboot(node) {
		glog.Info("Reboot requested...")

		// Set "reboot in progress" and clear reboot needed / reboot
		glog.Info("Setting `reboot-in-progress` annotations and removing others")
		node.Annotations[common.RebootInProgressAnnotation] = ""
		delete(node.Annotations, common.RebootNeededAnnotation)
		delete(node.Annotations, common.RebootAnnotation)

		glog.Info("Cordoning node...")
		a.cordonNode(node)

		// Update the node object
		_, err := a.client.Core().Nodes().Update(node)
		if err != nil {
			glog.Errorf("Failed to update node: %v", err)
			return
		}

		glog.Info("Reallocating pods...")
		err = a.removePods(node)
		if err != nil {
			glog.Errorf("%v", err)
		}

		glog.Infof("Rebooting node...")
		a.dbusConn.Reboot(false)
		select {} // Wait for machine to reboot
	}

	// Reboot complete - clear the rebootInProgress annotation
	// This is a niave assumption: the call to reboot is blocking - if we've reached this, assume the node has restarted.
	if rebootInProgress(node) {
		glog.Info("Clearing in-progress reboot annotation")
		delete(node.Annotations, common.RebootInProgressAnnotation)

		glog.Info("Re-marking node as Schedulable")
		node.Spec.Unschedulable = false

		_, err := a.client.Core().Nodes().Update(node)
		if err != nil {
			glog.Errorf("Failed to remove %s annotation: %v", common.RebootInProgressAnnotation, err)
			return
		}
	}
}

func shouldReboot(node *v1.Node) bool {
	_, reboot := node.Annotations[common.RebootAnnotation]
	_, inProgress := node.Annotations[common.RebootInProgressAnnotation]

	return reboot && !inProgress
}

func rebootInProgress(node *v1.Node) bool {
	_, inProgress := node.Annotations[common.RebootInProgressAnnotation]
	return inProgress
}
