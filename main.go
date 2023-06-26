package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"

	authorizationv1 "k8s.io/api/authorization/v1"
	"k8s.io/client-go/discovery"
	authorizationv1client "k8s.io/client-go/kubernetes/typed/authorization/v1"

	"k8s.io/client-go/metadata"
	"k8s.io/client-go/metadata/metadatainformer"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/homedir"
)

type self struct {
	client authorizationv1client.SelfSubjectAccessReviewInterface
}

func main() {
	// flags
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
		}
	}

	// client
	metadataClient, err := metadata.NewForConfig(config)
	if err != nil {
		fmt.Printf("error %s in getting dynamic client\n", err.Error())
	}

	// discovery client
	discoveryClient, err := discovery.NewDiscoveryClientForConfig(config)
	if err != nil {
		fmt.Printf("error %s in getting discovery client\n", err.Error())
	}

	authClient, err := authorizationv1client.NewForConfig(config)

	if(err != nil){
		fmt.Printf("error %s in getting authorization client\n", err.Error())
	}

	// factory
	infFactory := metadatainformer.NewFilteredSharedInformerFactory(metadataClient, 10*time.Minute, "", func(options *metav1.ListOptions) {
		options.LabelSelector = "kyverno.io/ttl"
	})
	defer infFactory.Shutdown()

	// discover resources
	resources, err := discoverResources(discoveryClient)

	if err != nil {
		fmt.Printf("error %s in discovering resources\n", err.Error())
	}

	// filter out the resources that are allowed to get, list and deleted by the service account
	var validResources []schema.GroupVersionResource
	validResources = filterPermissionsResource(resources, *authClient)

	// context
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	// controllers
	var controllers []*controller
	for _, resource := range validResources {
		// informer := infFactory.ForResource(resource)
		// client := metadataClient.Resource(resource)
		controller :=  reconcileController(resource, metadataClient, infFactory)
		controllers = append(controllers, controller)
	}

	// cache sync
	infFactory.Start(ctx.Done())
	infFactory.WaitForCacheSync(ctx.Done())

	// run
	for _, controller := range controllers {
		go controller.run(ctx)
	}

	// Watch for new resource additions
	go watchResourceAdditions(ctx, validResources, metadataClient, infFactory, discoveryClient)

	// shutdown
	<-ctx.Done()
}

func reconcileController(resource schema.GroupVersionResource, metadataClient metadata.Interface, infFactory metadatainformer.SharedInformerFactory) *controller {
	client := metadataClient.Resource(resource)
	informer := infFactory.ForResource(resource)
	return newController(client, informer)
}


func watchResourceAdditions(ctx context.Context, resources []schema.GroupVersionResource, metadataClient metadata.Interface, infFactory metadatainformer.SharedInformerFactory, discoveryClient *discovery.DiscoveryClient) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
			// Check if any new resources have been added
			newResources, err := discoverResources(discoveryClient)
			if err != nil {
				log.Printf("Error discovering resources: %v", err)
				continue
			}

			// Find new resources that are not already being controlled
			for _, newResource := range newResources {
				if !containsResource(resources, newResource) {
					fmt.Printf("detected a new resource: %s\ncreating a controller for the same\n", newResource.String())
					controller := reconcileController(newResource, metadataClient, infFactory)
					go controller.run(ctx)
					resources = append(resources, newResource)
				}
			}

			time.Sleep(1 * time.Minute) // Adjust the sleep duration as per your requirement
		}
	}
}

func containsResource(resources []schema.GroupVersionResource, resource schema.GroupVersionResource) bool {
	for _, r := range resources {
		if r == resource {
			return true
		}
	}
	return false
}


func discoverResources( discoveryClient discovery.DiscoveryInterface) ([]schema.GroupVersionResource, error) {
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

func filterPermissionsResource(resources[] schema.GroupVersionResource, authClient authorizationv1client.AuthorizationV1Client) ([]schema.GroupVersionResource) {
	 validResources := []schema.GroupVersionResource{}
	 s := self{
		client: authClient.SelfSubjectAccessReviews(),
	}
	 for _, resource := range resources {
		// Check if the service account has the necessary permissions
		if(s.hasResourcePermissions(resource)){
			validResources = append(validResources, resource)
		}
		
	 }
	 return validResources
}

func (c self) hasResourcePermissions( resource schema.GroupVersionResource) bool {

	// Check if the service account has the required verbs (get, list, delete)
	verbs := []string{"get", "list", "delete"}
	for _, verb := range verbs {
		subjectAccessReview := &authorizationv1.SelfSubjectAccessReview{
			Spec: authorizationv1.SelfSubjectAccessReviewSpec{
				ResourceAttributes: &authorizationv1.ResourceAttributes{
					Namespace:  "default", // Set the appropriate namespace
					Verb:       verb,
					Group:      resource.Group,
					Version:    resource.Version,
					Resource:   resource.Resource,
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

