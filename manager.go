package main

import (
	"context"
	"fmt"
	"log"
	"time"

	checker "github.com/kyverno/kyverno/pkg/auth/checker"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/discovery"
	authorizationv1client "k8s.io/client-go/kubernetes/typed/authorization/v1"
	"k8s.io/client-go/metadata"
	"k8s.io/client-go/metadata/metadatainformer"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
)

type manager struct {
	metadataClient  metadata.Interface
	discoveryClient discovery.DiscoveryInterface
	checker         checker.AuthChecker
	wg              wait.Group
	resController   map[schema.GroupVersionResource]*controller
	stopChans       map[schema.GroupVersionResource]chan struct{} // To track stop signals for each informer
}

func NewManager(config *rest.Config) (*manager, error) {
	// client
	metadataClient, err := metadata.NewForConfig(config)
	if err != nil {
		return nil, err
	}

	// discovery client
	discoveryClient, err := discovery.NewDiscoveryClientForConfig(config)
	if err != nil {
		return nil, err
	}

	authClient, err := authorizationv1client.NewForConfig(config)

	if err != nil {
		return nil, err
	}

	// checker

	selfChecker := checker.NewSelfChecker(authClient.SelfSubjectAccessReviews())

	// factory
	// infFactory := metadatainformer.NewFilteredSharedInformerFactory(metadataClient, 10*time.Minute, "", func(options *metav1.ListOptions) {
	// 	options.LabelSelector = "kyverno.io/ttl"
	// })
	// defer infFactory.Shutdown()

	//map initialization
	resController := make(map[schema.GroupVersionResource]*controller)

	// Initialize stop channels map
	stopChans := make(map[schema.GroupVersionResource]chan struct{})

	return &manager{
		metadataClient:  metadataClient,
		discoveryClient: discoveryClient,
		checker:         selfChecker,
		wg:              wait.Group{},
		resController:   resController,
		stopChans:       stopChans,
	}, nil
}

func (m *manager) Run(ctx context.Context) error {
	defer func() {
		// Stop all informers and wait for them to finish
		for gvr := range m.stopChans {
			if err := m.stop(ctx, gvr); err != nil {
				log.Println("Error stopping informer:", err)
			}
		}
		m.wg.Wait()
	}()

	for {
		select {
		case <-ctx.Done():
			return nil
		default:
			if err := m.reconcile(ctx); err != nil {
				return err
			}
			time.Sleep(1 * time.Minute)
		}
	}
}

func (m *manager) getDesiredState() (sets.Set[schema.GroupVersionResource], error) {
	// Get the list of resources currently present in the cluster
	newresources, err := discoverResources(m.discoveryClient)
	if err != nil {
		return nil, err
	}
	validResources := m.filterPermissionsResource(newresources)
	return sets.New(validResources...), nil
}

func (m *manager) getObservedState() (sets.Set[schema.GroupVersionResource], error) {
	observedState := sets.New[schema.GroupVersionResource]()
	for resource := range m.resController {
		observedState.Insert(resource)
	}
	return observedState, nil
}

func (m *manager) stop(ctx context.Context, gvr schema.GroupVersionResource) error {
	if controller, ok := m.resController[gvr]; ok {
		delete(m.resController, gvr)
		stopChan, ok := m.stopChans[gvr]
		if ok {
			close(stopChan) // Send stop signal to informer's goroutine
			delete(m.stopChans, gvr)
		}
		log.Println("Informer stopped ", gvr)
		controller.Stop()
	}
	return nil
}

func (m *manager) start(ctx context.Context, gvr schema.GroupVersionResource) error {
	indexers := cache.Indexers{
		cache.NamespaceIndex: cache.MetaNamespaceIndexFunc,
	}
	options := func(options *metav1.ListOptions) {
		options.LabelSelector = "kyverno.io/ttl"
	}

	informer := metadatainformer.NewFilteredMetadataInformer(m.metadataClient,
		gvr,
		metav1.NamespaceAll,
		10*time.Minute,
		indexers,
		options,
	)

	controller := newController(m.metadataClient.Resource(gvr), informer)

	stopChan := make(chan struct{}) // Create a stop signal channel
	m.stopChans[gvr] = stopChan     // Store the stop channel

	m.wg.StartWithContext(ctx, func(ctx context.Context) {
		defer log.Println("informer started", gvr)
		log.Println("informer starting...", gvr)
		informer.Informer().Run(stopChan)
	})

	if !cache.WaitForCacheSync(ctx.Done(), informer.Informer().HasSynced) {
		return fmt.Errorf("failed to wait for cache sync: %s", gvr)
	}

	log.Println("controller starting...", gvr)
	controller.Start(ctx, 3)
	m.resController[gvr] = controller
	return nil
}

func (m *manager) reconcile(ctx context.Context) error {
	log.Println("start manager reconciliation")
	desiredState, err := m.getDesiredState()
	if err != nil {
		return err
	}
	observedState, err := m.getObservedState()
	if err != nil {
		return err
	}
	for gvr := range observedState.Difference(desiredState) {
		if err := m.stop(ctx, gvr); err != nil {
			return err
		}
	}
	for gvr := range desiredState.Difference(observedState) {
		if err := m.start(ctx, gvr); err != nil {
			return err
		}
	}
	return nil
}
