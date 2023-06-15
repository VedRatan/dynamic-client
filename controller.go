package main

import (
	"context"
	"fmt"
	"time"

	"github.com/VedRatan/kluster/pkg/apis/vedratan.dev/v1alpha1"
	"github.com/gardener/controller-manager-library/pkg/logger"
	//"k8s.io/apimachinery/pkg/api/meta"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/wait"

	// "k8s.io/client-go/dynamic"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/metadata"
	"k8s.io/client-go/util/workqueue"

	// "k8s.io/client-go/dynamic/dynamicinformer"
	"k8s.io/client-go/tools/cache"
)

type controller struct {
	client   metadata.Interface
	informer cache.SharedIndexInformer
	queue    workqueue.RateLimitingInterface
	lister   cache.GenericLister
}

func newController(client metadata.Interface, metainformer informers.GenericInformer, resource schema.GroupVersionResource) *controller {
	// inf := dynInformerFactory.ForResource(schema.GroupVersionResource{
	// 	Group:    "vedratan.dev",
	// 	Version:  "v1alpha1",
	// 	Resource: "klusters",
	// }).Informer()

	inf := metainformer.Informer()
	lister := metainformer.Lister()

	c := &controller{
		client:   client,
		informer: inf,
		queue:    workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "kluster"),
		lister: lister,
	}

	inf.AddEventHandler(
		cache.ResourceEventHandlerFuncs{
			AddFunc:    c.handleAdd,
			DeleteFunc: c.handleDelete,
			UpdateFunc: c.handleUpdate,
		},
	)
	return c

}

func (c *controller) handleAdd(obj interface{}) {
	// metaObj, err := meta.Accessor(obj)
	// if err != nil {
	// 	fmt.Printf("Error accessing metadata: %v", err)
	// 	return
	// }

	// Get the resource's labels
	// labels := metaObj.GetLabels()

	fmt.Println("resource was created")
	// Check if the resource has a specific label
	// if val, ok := labels["foo"]; ok {
	// 	fmt.Printf("Resource with label 'foo' was created: %s\n", val)
		key, err := cache.MetaNamespaceKeyFunc(obj)
		if err != nil {
			logger.Error(err, "failed to extract name")
			return
		}
		c.queue.Add(key)
	// }
}

func (c *controller) handleDelete(obj interface{}) {
	// metaObj, err := meta.Accessor(obj)
	// if err != nil {
	// 	fmt.Printf("Error accessing metadata: %v", err)
	// 	return
	// }

	// Get the resource's labels
	// labels := metaObj.GetLabels()

	// fmt.Println("resource was created")
	// Check if the resource has a specific label
		// fmt.Printf("Resource with label 'foo' was deleted: %s\n", labels["foo"])
		key, err := cache.MetaNamespaceKeyFunc(obj)
		if err != nil {
			logger.Error(err, "failed to extract name")
			return
		}
		c.queue.Add(key)
}

func (c *controller) handleUpdate(oldObj, newObj interface{}) {
	// oldMetaObj, err := meta.Accessor(oldObj)
	// if err != nil {
	// 	log.Printf("Error accessing old metadata: %v", err)
	// 	return
	// }
	// newMetaObj, err := meta.Accessor(newObj)
	// if err != nil {	
	// 	log.Printf("Error accessing new metadata: %v", err)
	// 	return
	// }

	// Get the old and new resource's labels
	// oldLabels := oldMetaObj.GetLabels()
	// newLabels := newMetaObj.GetLabels()

	// Check if the label value has changed
	// if oldVal, ok := oldLabels["foo"]; ok {
	// 	if newVal, ok := newLabels["foo"]; ok && oldVal != newVal {
	// 		fmt.Printf("Resource with label 'foo' was updated: %s -> %s\n", oldVal, newVal)

			key, err := cache.MetaNamespaceKeyFunc(newObj)
			if err != nil {
				logger.Error(err, "failed to extract name")
				return
			}
	 		c.queue.Add(key)
	// 	}
	// }
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
	item, shutdown := c.queue.Get()
	if shutdown {
		return false
	}

	fmt.Printf("%+v\n", item)
	defer c.queue.Forget(item)
	err := c.reconcile(item.(string))
	if err != nil {
		fmt.Printf("reconciliation failed err: %s, for resource %s\n", err.Error(), item)
		c.queue.AddRateLimited(item)
		return true
	}
	c.queue.Done(item)
	return true
}

func (c *controller) reconcile(itemKey string) error {
	obj, exists, err := c.informer.GetIndexer().GetByKey(itemKey)
	resource := v1alpha1.SchemeGroupVersion.WithResource("klusters")
	if err != nil {
		return fmt.Errorf("failed to get object '%s': %w", itemKey, err)
	}

	if !exists {
		// Resource has been deleted, no further action needed
		return nil
	}

	// Assuming the object is of type metav1.Object, you can replace it with the correct type
	metaObj, ok := obj.(v1.Object)
	// fmt.Printf("the object is: %+v\n", metaObj)
	
	if !ok {
		return fmt.Errorf("object '%s' is not of type metav1.Object", itemKey)
	}

	labels := metaObj.GetLabels()
	ttlValue, ok := labels["ttl"]

	if !ok {
		// No 'ttl' label present, no further action needed
		return nil
	}

	ttlDuration, err := time.ParseDuration(ttlValue)
	if err != nil {
		// Invalid TTL duration, log an error and skip deletion
		logger.Error(err, "failed to parse TTL duration", "item", itemKey, "ttlValue", ttlValue)
		return nil
	}

	creationTime := metaObj.GetCreationTimestamp().Time
	deletionTime := creationTime.Add(ttlDuration)

	fmt.Printf("the time to expire is: %s\n", deletionTime)

	err = c.client.Resource(resource).Delete(context.Background(), metaObj.GetName(), v1.DeleteOptions{})
		if err != nil {
			return fmt.Errorf("failed to delete object '%s': %w", itemKey, err)
		}

		fmt.Printf("Resource '%s' has been deleted\n", itemKey)

	// if time.Now().After(deletionTime) {
	// 	// Perform the deletion of the resource
	// 	err := c.client.Resource(resource).Delete(context.Background(), metaObj.GetName(), v1.DeleteOptions{})
	// 	if err != nil {
	// 		return fmt.Errorf("failed to delete object '%s': %w", itemKey, err)
	// 	}

	// 	fmt.Printf("Resource '%s' has been deleted\n", itemKey)
	// }
	return nil
}
