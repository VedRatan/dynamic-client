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
	var resources []schema.GroupVersionResource

	// context
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	 // controllers
	 var controllers []controller

	// cache sync
	infFactory.Start(ctx.Done())
	infFactory.WaitForCacheSync(ctx.Done())

	// Watch for new resource additions
	go watchResourceAdditions(ctx, &resources, &controllers, metadataClient, infFactory, discoveryClient, *authClient)

	// shutdown
	<-ctx.Done()
}

func reconcileController(resource schema.GroupVersionResource,metadataClient metadata.Interface, infFactory metadatainformer.SharedInformerFactory) *controller {
	client := metadataClient.Resource(resource)
	informer := infFactory.ForResource(resource)
	return newController(client, informer)
}


func watchResourceAdditions(ctx context.Context, resources *[]schema.GroupVersionResource, controllers *[]controller,metadataClient metadata.Interface, infFactory metadatainformer.SharedInformerFactory, discoveryClient *discovery.DiscoveryClient, authClient authorizationv1client.AuthorizationV1Client) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
			s := self{
				client: authClient.SelfSubjectAccessReviews(),
			}
			// Check if any new resources have been added
			newResources, err := discoverResources(discoveryClient)
			if err != nil {
				fmt.Printf("error %s in discovering resources\n", err.Error())
			}
		
			// Find new resources that are not already being controlled
			for _, newResource := range newResources {
				if !containsResource(*resources, newResource) {
					log.Printf("New resource added: %s", newResource.String())

					//check if we can list, delete and get the resource by the service account mounted
					if(s.hasResourcePermissions(newResource)){
						controller := reconcileController(newResource, metadataClient, infFactory)
					go controller.run(ctx)
					*resources = append(*resources, newResource)
					*controllers = append(*controllers, *controller)
					}
				}
			}

			// Find resources that have been removed
			update := false
			for _, resource := range *resources {
				if !containsResource(newResources, resource) {
					log.Printf("Resource removed: %s", resource.String())
					update = true
					// Handle the removal of the resource here
					// For example, stop the corresponding controller
				}
			}

			if update{
				*resources = newResources
				update = false
			}

			time.Sleep(1 * time.Minute) // to watch the controller state at a particular interval of time
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

