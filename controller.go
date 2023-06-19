package main

import (
	"context"
	"fmt"
	"time"
	"github.com/gardener/controller-manager-library/pkg/logger"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/wait"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/metadata"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/client-go/tools/cache"
)

type controller struct {
	client   metadata.Interface
	informer cache.SharedIndexInformer
	queue    workqueue.RateLimitingInterface
	lister   cache.GenericLister
	resource schema.GroupVersionResource
}

func newController(client metadata.Interface, metainformer informers.GenericInformer, resource schema.GroupVersionResource, resourceName string) *controller {

	inf := metainformer.Informer()
	lister := metainformer.Lister()
	fmt.Printf("%s \n",resource)

	c := &controller{
		client:   client,
		informer: inf,
		queue:    workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), resourceName),
		lister:   lister,
		resource: resource,
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

	fmt.Println("resource was created")
	key, err := cache.MetaNamespaceKeyFunc(obj)
	if err != nil {
		logger.Error(err, "failed to extract name")
		return
	}
	c.queue.Add(key)
}

func (c *controller) handleDelete(obj interface{}) {
	key, err := cache.MetaNamespaceKeyFunc(obj)
	if err != nil {
		logger.Error(err, "failed to extract name")
		return
	}
	c.queue.Add(key)
}

func (c *controller) handleUpdate(oldObj, newObj interface{}) {
	
	key, err := cache.MetaNamespaceKeyFunc(newObj)
	if err != nil {
		logger.Error(err, "failed to extract name")
		return
	}
	c.queue.Add(key)
}

func (c *controller) run(ch <-chan struct{}) {
	fmt.Println("starting controller ....")
	go wait.Until(c.worker, 1*time.Second, ch)

	// this will prevent the go coroutine from exiting or stopping
	<-ch
}

func (c *controller) worker() {
	for {
		if !c.processItem() {
			// No more items in the queue, exit the loop
			break
		}
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
	namespace, name, err := cache.SplitMetaNamespaceKey(itemKey)
	if err != nil {
		return err
	}
	obj, err := c.lister.ByNamespace(namespace).Get(name)
	if err != nil {
		if apierrors.IsNotFound(err) {
			// resource doesn't exist anymore, nothing much to do at this point
			return nil
		}
		// there was an error, return it to requeue the key
		return err
	}
	// we now know the observed state, check against the desired state...
	// Assuming the object is of type metav1.Object, you can replace it with the correct type
	metaObj, error := meta.Accessor(obj)
	// fmt.Printf("the object is: %+v\n", metaObj)

	if error != nil {
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


	if time.Now().After(deletionTime) {
		err = c.client.Resource(c.resource).Namespace(namespace).Delete(context.Background(), metaObj.GetName(), v1.DeleteOptions{})
		if err != nil {
			return fmt.Errorf("failed to delete object '%s': %w", itemKey, err)
		}
		fmt.Printf("Resource '%s' has been deleted\n", itemKey)
	} else {
		// Calculate the remaining time until deletion
		timeRemaining := deletionTime.Sub(time.Now())
		// Add the item back to the queue after the remaining time
		c.queue.AddAfter(itemKey, timeRemaining)
	}
	 return nil
}