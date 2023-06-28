package main

import (
	"context"
	"log"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/discovery"
	authorizationv1client "k8s.io/client-go/kubernetes/typed/authorization/v1"
	"k8s.io/client-go/metadata"
	"k8s.io/client-go/metadata/metadatainformer"
	"k8s.io/client-go/rest"
)

type manager struct {
	metadataClient metadata.Interface
	discoveryClient discovery.DiscoveryInterface
	authClient      *authorizationv1client.AuthorizationV1Client
	infFactory      metadatainformer.SharedInformerFactory
}


type self struct {
	client authorizationv1client.SelfSubjectAccessReviewInterface
}

var resources[] schema.GroupVersionResource
var controllers[] controller

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

	// factory
	infFactory := metadatainformer.NewFilteredSharedInformerFactory(metadataClient, 10*time.Minute, "", func(options *metav1.ListOptions) {
		options.LabelSelector = "kyverno.io/ttl"
	})
	defer infFactory.Shutdown()

	return &manager{
		metadataClient: metadataClient,
		discoveryClient: discoveryClient,
		authClient:      authClient,
		infFactory:      infFactory,
	}, nil
}

func (m *manager) Run(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			return nil
		default:
			// cache sync
			m.infFactory.Start(ctx.Done())
			m.infFactory.WaitForCacheSync(ctx.Done())

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

	validResources := filterPermissionsResource(newresources, *m.authClient)

	// Convert the list of resources to a set for easy comparison
	desiredState := sets.New[schema.GroupVersionResource]()
	for _, resource := range validResources {
		desiredState.Insert(resource)
	}

	return desiredState, nil
}

func (m *manager) getObservedState() (sets.Set[schema.GroupVersionResource], error) {
		observedState := sets.New[schema.GroupVersionResource]()
		for _, resource := range resources {
			observedState.Insert(resource)
		}
		return observedState, nil
}

func (m *manager) stop(gvr schema.GroupVersionResource, ctx context.Context) error {
	// TODO

	for _, controller := range controllers{
		if controller.resource  == gvr {
			controller.stop()
			return nil
		}
	}

	return nil
}

func (m *manager) start(gvr schema.GroupVersionResource, ctx context.Context) error {
	controller:= createController(gvr, m.metadataClient, m.infFactory)
	log.Printf("Starting controller for resource: %s", gvr.String())
	go controller.run(ctx)
	controllers = append(controllers, *controller)
	resources = append(resources, gvr)
	return nil
}

func createController(resource schema.GroupVersionResource, metadataClient metadata.Interface, infFactory metadatainformer.SharedInformerFactory) *controller {
	client := metadataClient.Resource(resource)
	informer := infFactory.ForResource(resource)
	return newController(client, informer, resource)
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
		if err := m.stop(gvr,ctx); err != nil {
			return err
		}
	}
	for gvr := range desiredState.Difference(observedState) {
		if err := m.start(gvr,ctx); err != nil {
			return err
		}
	}
	return nil
}

