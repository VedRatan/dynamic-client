package main

import (
	"fmt"
	"time"

	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/dynamic/dynamicinformer"
	"k8s.io/client-go/tools/cache"
)

type controller struct {
	client   dynamic.Interface
	informer cache.SharedIndexInformer
}

func newController(client dynamic.Interface, dynInformerFactory dynamicinformer.DynamicSharedInformerFactory) *controller {
	inf := dynInformerFactory.ForResource(schema.GroupVersionResource{
		Group:    "vedratan.dev",
		Version:  "v1alpha1",
		Resource: "klusters",
	}).Informer()

	inf.AddEventHandler(
		cache.ResourceEventHandlerFuncs{
			AddFunc: func(obj interface{}) {
				fmt.Println("resource was created")
			},
			DeleteFunc: func (obj interface{})  {
				fmt.Println("resource was deleted")
			},
		},
	)

	return &controller{
		client:   client,
		informer: inf,
	}

}


func (c *controller) run(ch <-chan struct{}) {
	fmt.Println("starting controller ....")
	if !cache.WaitForCacheSync(ch, c.informer.HasSynced){
		fmt.Print("waiting for the cache to be synced...\n")
	}

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
