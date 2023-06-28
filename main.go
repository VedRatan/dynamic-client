package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	authorizationv1 "k8s.io/api/authorization/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/discovery"
	authorizationv1client "k8s.io/client-go/kubernetes/typed/authorization/v1"
	"k8s.io/client-go/metadata"
	"k8s.io/client-go/metadata/metadatainformer"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/homedir"
)

type manager struct {
	metadataClient metadata.Interface
	discoveryClient discovery.DiscoveryClient
	authClient      *authorizationv1client.AuthorizationV1Client
	infFactory      metadatainformer.SharedInformerFactory
}

type self struct {
	client authorizationv1client.SelfSubjectAccessReviewInterface
}

var resources[] schema.GroupVersionResource
var controllers[] controller

func main() {
	var kubeconfig *string

	if home := homedir.HomeDir(); home != "" {
		kubeconfig = flag.String("kubeconfig", filepath.Join(home, ".kube", "config"), "(optional) absolute path to the kubeconfig file")
	} else {
		kubeconfig = flag.String("kubeconfig", "", "absolute path to the kubeconfig file")
	}
	flag.Parse()

	// config
	config, err := clientcmd.BuildConfigFromFlags("", *kubeconfig)
	if err != nil {
		log.Printf("Building config from flags failed, %s, trying to build inclusterconfig", err.Error())
		config, err = rest.InClusterConfig()
		if err != nil {
			log.Printf("error %s building inclusterconfig", err.Error())
			os.Exit(1)
		}
	}
	// manager
	manager, err := NewManager(config)
	if err != nil {
		log.Printf("error %s creating manager", err.Error())
		os.Exit(1)
	}

	// context
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	// run
	if err := manager.Run(ctx); err != nil {
		log.Printf("error %s running manager", err.Error())
		os.Exit(1)
	}
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

	// factory
	infFactory := metadatainformer.NewFilteredSharedInformerFactory(metadataClient, 10*time.Minute, "", func(options *metav1.ListOptions) {
		options.LabelSelector = "kyverno.io/ttl"
	})
	defer infFactory.Shutdown()

	return &manager{
		metadataClient: metadataClient,
		discoveryClient: *discoveryClient,
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
	newresources, err := discoverResources(&m.discoveryClient)
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
	return newController(client, informer)
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


func discoverResources(discoveryClient discovery.DiscoveryInterface) ([]schema.GroupVersionResource, error) {
	resources := []schema.GroupVersionResource{}

	apiResourceList, err := discoveryClient.ServerPreferredResources()
	if err != nil {
		if discovery.IsGroupDiscoveryFailedError(err) { // the error should be recoverable, let's log missing groups and process the partial results we received
			err := err.(*discovery.ErrGroupDiscoveryFailed)
			for gv, groupErr := range err.Groups {
				// Handling the specific group error
				log.Printf("Error in discovering group %s: %v", gv.String(), groupErr)
			}
		} else { // if not a discovery error we should return early
			// Handling other non-group-specific errors
			return nil, err
		}
	}
	requiredVerbs := []string{"list", "watch", "delete"}
	for _, apiResourceList := range apiResourceList {
		for _, apiResource := range apiResourceList.APIResources {
			if containsAllVerbs(apiResource.Verbs, requiredVerbs) {
				groupVersion, err := schema.ParseGroupVersion(apiResourceList.GroupVersion)
				if err != nil {
					return resources, err
				}

				resource := schema.GroupVersionResource{
					Group:    groupVersion.Group,
					Version:  groupVersion.Version,
					Resource: apiResource.Name,
				}

				resources = append(resources, resource)
			}
		}
	}

	return resources, nil
}

func filterPermissionsResource(resources []schema.GroupVersionResource, authClient authorizationv1client.AuthorizationV1Client) []schema.GroupVersionResource {
	validResources := []schema.GroupVersionResource{}
	s := self{
		client: authClient.SelfSubjectAccessReviews(),
	}
	for _, resource := range resources {
		// Check if the service account has the necessary permissions
		if s.hasResourcePermissions(resource) {
			validResources = append(validResources, resource)
		}

	}
	return validResources
}

func (c self) hasResourcePermissions(resource schema.GroupVersionResource) bool {

	// Check if the service account has the required verbs (get, list, delete)
	verbs := []string{"get", "list", "delete"}
	for _, verb := range verbs {
		subjectAccessReview := &authorizationv1.SelfSubjectAccessReview{
			Spec: authorizationv1.SelfSubjectAccessReviewSpec{
				ResourceAttributes: &authorizationv1.ResourceAttributes{
					Namespace: "default", // Set the appropriate namespace
					Verb:      verb,
					Group:     resource.Group,
					Version:   resource.Version,
					Resource:  resource.Resource,
				},
			},
		}
		result, err := c.client.Create(context.TODO(), subjectAccessReview, metav1.CreateOptions{})
		if err != nil {
			log.Printf("Failed to check resource permissions: %v", err)
			return false
		}
		if !result.Status.Allowed {
			log.Printf("Service account does not have '%s' permission for resource: %s", verb, resource.Resource)
			return false
		}
	}

	return true
}

func containsAllVerbs(supportedVerbs []string, requiredVerbs []string) bool {
	for _, requiredVerb := range requiredVerbs {
		found := false
		for _, verb := range supportedVerbs {
			if verb == requiredVerb {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}
