package main

import (
	"fmt"
	"log"
	"time"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/wait"

	// "k8s.io/client-go/dynamic"
	"k8s.io/client-go/metadata"
	"k8s.io/client-go/metadata/metadatainformer"
	"k8s.io/client-go/util/workqueue"

	// "k8s.io/client-go/dynamic/dynamicinformer"
	"k8s.io/client-go/tools/cache"
)

type controller struct {
	client   metadata.Interface
	informer cache.SharedIndexInformer
	queue            workqueue.RateLimitingInterface
}

func newController(client metadata.Interface, metaInformerFactory metadatainformer.SharedInformerFactory, resource schema.GroupVersionResource) *controller {
	// inf := dynInformerFactory.ForResource(schema.GroupVersionResource{
	// 	Group:    "vedratan.dev",
	// 	Version:  "v1alpha1",
	// 	Resource: "klusters",
	// }).Informer()

	inf := metaInformerFactory.ForResource(resource).Informer()

	c := &controller{
		client:   client,
		informer: inf,
		queue: workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "kluster"),
	}

	inf.AddEventHandler(
		cache.ResourceEventHandlerFuncs{
			AddFunc: c.handleAdd,
			DeleteFunc: c.handleDelete,
			UpdateFunc: c.handleUpdate,
		},
	)
	return c

}

func (c *controller) handleAdd(obj interface{}) {
		metaObj, err := meta.Accessor(obj)
		if err != nil {
			fmt.Printf("Error accessing metadata: %v", err)
			return
		}

		// Get the resource's labels
		 labels := metaObj.GetLabels()

		 fmt.Println("resource was created")
		// Check if the resource has a specific label
		 if val, ok := labels["foo"]; ok {
			fmt.Printf("Resource with label 'foo' was created: %s\n", val)
			c.queue.Add(obj)
		 }
}

func (c *controller) handleDelete(obj interface{}){
	metaObj, err := meta.Accessor(obj)
	if err != nil {
		fmt.Printf("Error accessing metadata: %v", err)
		return
	}

	// Get the resource's labels
	labels := metaObj.GetLabels()

	// fmt.Println("resource was created")
	// Check if the resource has a specific label
	if val, ok := labels["foo"]; ok {
		fmt.Printf("Resource with label 'foo' was deleted: %s\n", val)
	}
}

func (c *controller) handleUpdate(oldObj, newObj interface{}){
	oldMetaObj, err := meta.Accessor(oldObj)
	if err != nil {
		log.Printf("Error accessing old metadata: %v", err)
		return
	}
	newMetaObj, err := meta.Accessor(newObj)
	if err != nil {
		log.Printf("Error accessing new metadata: %v", err)
		return
	}

	// Get the old and new resource's labels
	oldLabels := oldMetaObj.GetLabels()
	newLabels := newMetaObj.GetLabels()

	// Check if the label value has changed
	if oldVal, ok := oldLabels["foo"]; ok {
		if newVal, ok := newLabels["foo"]; ok && oldVal != newVal {
			fmt.Printf("Resource with label 'foo' was updated: %s -> %s\n", oldVal, newVal)
		}
	}
}

func (c *controller) run(ch <-chan struct{}) {
	fmt.Println("starting controller ....")
	// if !cache.WaitForCacheSync(ch, c.informer.HasSynced) {
	// 	fmt.Print("waiting for the cache to be synced...\n")
	// }

	// launching a go coroutine to run continuously till terminated by the end user, it will resync the cache immediately after 1s
	go wait.Until(c.worker, 1*time.Second, ch)

	// this will prevent the go coroutine from exiting or stopping
	<-ch
}

func (c *controller) worker() {
	for c.processItem() {

	}
}

func (c *controller) processItem() bool {
	return true
}
