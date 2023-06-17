package main

import (
	"context"
	"fmt"
	"time"

	"github.com/VedRatan/kluster/pkg/apis/vedratan.dev/v1alpha1"
	"github.com/gardener/controller-manager-library/pkg/logger"

	//"k8s.io/apimachinery/pkg/api/meta"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	 corev1 "k8s.io/api/core/v1"

	"k8s.io/apimachinery/pkg/util/wait"

	// "k8s.io/client-go/dynamic"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/metadata"
	"k8s.io/client-go/util/workqueue"

	// "k8s.io/client-go/dynamic/dynamicinformer"
	"k8s.io/client-go/tools/cache"
)

type controller struct {
	client          metadata.Interface
	klusterinformer cache.SharedIndexInformer
	podinformer     cache.SharedIndexInformer
	cminformer      cache.SharedIndexInformer
	klusterqueue    workqueue.RateLimitingInterface
	podqueue        workqueue.RateLimitingInterface
	configmapqueue  workqueue.RateLimitingInterface
	klusterlister   cache.GenericLister
	podlister       cache.GenericLister
	configmaplister cache.GenericLister
}

func newController(client metadata.Interface, klusterinformer informers.GenericInformer, podinformer informers.GenericInformer, configmapinformer informers.GenericInformer) *controller {

	klusterinf := klusterinformer.Informer()
	klusterlist := klusterinformer.Lister()

	podinf := podinformer.Informer()
	podlist := podinformer.Lister()

	configmapinf := configmapinformer.Informer()
	configmaplist := configmapinformer.Lister()

	c := &controller{
		client: client,

		klusterqueue: workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "kluster"),
		podqueue: workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "pod"),
		configmapqueue: workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "configmap"),

		klusterinformer: klusterinf,
		klusterlister:   klusterlist,

		podinformer: podinf,
		podlister:   podlist,

		cminformer:      configmapinf,
		configmaplister: configmaplist,
	}

	klusterinf.AddEventHandler(
		cache.ResourceEventHandlerFuncs{
			AddFunc:    c.handleAdd,
			DeleteFunc: c.handleDelete,
			UpdateFunc: c.handleUpdate,
		},
	)

	podinf.AddEventHandler(
		cache.ResourceEventHandlerFuncs{
			AddFunc:    c.handleAdd,
			DeleteFunc: c.handleDelete,
			UpdateFunc: c.handleUpdate,
		},
	)

	configmapinf.AddEventHandler(
		cache.ResourceEventHandlerFuncs{
			AddFunc:    c.handleAdd,
			DeleteFunc: c.handleDelete,
			UpdateFunc: c.handleUpdate,
		},
	)
	return c

}

func (c *controller) handleAdd(obj interface{}) {
	fmt.Printf("the resource has been created\n")
	if pod, ok := obj.(*corev1.Pod); ok {
		// Handle Pod resource
		key, err := cache.MetaNamespaceKeyFunc(pod)
		if err != nil {
			logger.Error(err, "Failed to extract pod key")
			return
		}
		c.podqueue.Add(key)
		fmt.Println("Enqueued pod:", key)
	} else if configMap, ok := obj.(*corev1.ConfigMap); ok {
		// Handle ConfigMap resource
		key, err := cache.MetaNamespaceKeyFunc(configMap)
		if err != nil {
			logger.Error(err, "Failed to extract configmap key")
			return
		}
		c.configmapqueue.Add(key)
		fmt.Println("Enqueued configmap:", key)
	} else if kluster, ok := obj.(*v1alpha1.Kluster); ok {
		// Handle Kluster resource
		key, err := cache.MetaNamespaceKeyFunc(kluster)
		if err != nil {
			logger.Error(err, "Failed to extract kluster key")
			return
		}
		c.klusterqueue.Add(key)
		fmt.Println("Enqueued kluster:", key)
	} else {
		logger.Info("Unknown resource type")
	}
}

func (c *controller) handleDelete(obj interface{}) {

	key, err := cache.MetaNamespaceKeyFunc(obj)
	if err != nil {
		logger.Error(err, "failed to extract name")
		return
	}
	c.klusterqueue.Add(key)
}

func (c *controller) handleUpdate(oldObj, newObj interface{}) {
	key, err := cache.MetaNamespaceKeyFunc(newObj)
	if err != nil {
		logger.Error(err, "failed to extract name")
		return
	}
	c.klusterqueue.Add(key)

}

func (c *controller) run(ch <-chan struct{}) {
	fmt.Println("starting controller ....")

	// launching a go coroutine to run continuously till terminated by the end user, it will resync the cache immediately after 1s
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
	item, shutdown := c.klusterqueue.Get()
	if shutdown {
		return false
	}

	// fmt.Printf("%+v\n", item)
	defer c.klusterqueue.Forget(item)
	err := c.reconcile(item.(string))
	if err != nil {
		fmt.Printf("reconciliation failed err: %s, for resource %s\n", err.Error(), item)
		c.klusterqueue.AddRateLimited(item)
		return true
	}
	c.klusterqueue.Done(item)
	return true
}

func (c *controller) reconcile(itemKey string) error {
	resource := v1alpha1.SchemeGroupVersion.WithResource("klusters")
	namespace, name, err := cache.SplitMetaNamespaceKey(itemKey)
	if err != nil {
		return err
	}
	obj, err := c.klusterlister.ByNamespace(namespace).Get(name)
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
		err = c.client.Resource(resource).Namespace(namespace).Delete(context.Background(), metaObj.GetName(), v1.DeleteOptions{})
		if err != nil {
			return fmt.Errorf("failed to delete object '%s': %w", itemKey, err)
		}
		fmt.Printf("Resource '%s' has been deleted\n", itemKey)
	} else {
		// Calculate the remaining time until deletion
		timeRemaining := deletionTime.Sub(time.Now())
		// Add the item back to the queue after the remaining time
		c.klusterqueue.AddAfter(itemKey, timeRemaining)
	}
	return nil
}
