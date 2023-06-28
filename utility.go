package main
 import ("context"
 "log"


 metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
 "k8s.io/apimachinery/pkg/runtime/schema"
 "k8s.io/client-go/discovery"
 authorizationv1client "k8s.io/client-go/kubernetes/typed/authorization/v1"
 authorizationv1 "k8s.io/api/authorization/v1"
)

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
	verbs := []string{"watch", "list", "delete"}
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