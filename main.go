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

	"github.com/VedRatan/kluster/pkg/apis/vedratan.dev/v1alpha1"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"k8s.io/apimachinery/pkg/runtime/schema"

	"k8s.io/client-go/metadata"
	"k8s.io/client-go/metadata/metadatainformer"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/homedir"
)

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

	// factory
	infFactory := metadatainformer.NewFilteredSharedInformerFactory(metadataClient, 10*time.Minute, "", func(options *metav1.ListOptions) {
		 options.LabelSelector = "kyverno.io/ttl"


		// options.LabelSelector = metav1.FormatLabelSelector(&metav1.LabelSelector{
		// 	MatchExpressions: []metav1.LabelSelectorRequirement{
		// 		{
		// 			Key:      "kyverno.io/ttl",
		// 			Operator: metav1.LabelSelectorOpExists,
		// 		},
		// 		{
		// 			Key:      "kyverno.io/expires",
		// 			Operator: metav1.LabelSelectorOpExists,
		// 		},
		// 	},
		// })

	})
	defer infFactory.Shutdown()

	// resources
	resources := []schema.GroupVersionResource{
		v1alpha1.SchemeGroupVersion.WithResource("klusters"),
		v1.SchemeGroupVersion.WithResource("pods"),
		v1.SchemeGroupVersion.WithResource("configmaps"),
	}

	// context
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	// controllers
	var controllers []*controller
	for _, resource := range resources {
		informer := infFactory.ForResource(resource)
		client := metadataClient.Resource(resource)
		controllers = append(controllers, newController(client, informer))
	}

	// cache sync
	infFactory.Start(ctx.Done())
	infFactory.WaitForCacheSync(ctx.Done())

	// run
	for _, controller := range controllers {
		go controller.run(ctx)
	}

	// shutdown
	<-ctx.Done()
}
