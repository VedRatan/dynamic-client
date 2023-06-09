package main

import (
	"context"
	"log"

	checker "github.com/kyverno/kyverno/pkg/auth/checker"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/discovery"
)

func discoverResources(discoveryClient discovery.DiscoveryInterface) ([]schema.GroupVersionResource, error) {
	var resources []schema.GroupVersionResource

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

	for _, apiResourceList := range apiResourceList {
		for _, apiResource := range apiResourceList.APIResources {
			if sets.NewString(apiResource.Verbs...).HasAll("list", "watch", "delete") {
				groupVersion, err := schema.ParseGroupVersion(apiResourceList.GroupVersion)
				if err != nil {
					return resources, err
				}
				resources = append(resources, groupVersion.WithResource(apiResource.Name))
			}
		}
	}

	return resources, nil
}

func (m *manager) filterPermissionsResource(resources []schema.GroupVersionResource) []schema.GroupVersionResource {
	validResources := []schema.GroupVersionResource{}
	for _, resource := range resources {
		// Check if the service account has the necessary permissions
		if hasResourcePermissions(resource, m.checker) {
			validResources = append(validResources, resource)
		}
	}
	return validResources
}

func hasResourcePermissions(resource schema.GroupVersionResource, s checker.AuthChecker) bool {
	can, err := checker.Check(context.TODO(), s, resource.Group, resource.Version, resource.Resource, " ", "default", "watch", "list", "delete")
	if err != nil {
		log.Println("failed to check permissions", err)
		return false
	}
	return can
}
