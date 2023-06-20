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

	"k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/metadata"
	"k8s.io/client-go/metadata/metadatainformer"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/homedir"
)

func main() {
	var kubeconfig *string

	if home := homedir.HomeDir(); home != "" {
		kubeconfig = flag.String("kubeconfig", filepath.Join(home, ".kube", "config"), "(optional) absolute path to the kubeconfig file")
	} else {
		kubeconfig = flag.String("kubeconfig", "", "absolute path to the kubeconfig file")
	}
	flag.Parse()

	config, err := clientcmd.BuildConfigFromFlags("", *kubeconfig)
	if err != nil {
		log.Printf("Building config from flags failed, %s, trying to build inclusterconfig", err.Error())
		config, err = rest.InClusterConfig()
		if err != nil {
			log.Printf("error %s building inclusterconfig", err.Error())
		}
	}

	metadataClient, err := metadata.NewForConfig(config)

	if err != nil {
		fmt.Printf("error %s in getting dynamic client\n", err.Error())
	}
	

	infFactory := metadatainformer.NewFilteredSharedInformerFactory(metadataClient, 10*time.Minute, "", func(options *metav1.ListOptions) {
		options.LabelSelector = "ttl"
	})
	

    kluster := v1alpha1.SchemeGroupVersion.WithResource("klusters")
	// klusterinformer := infFactory.ForResource(kluster)
	klusterController := initializeController(infFactory, metadataClient, kluster, "kluster")


	pod := v1.SchemeGroupVersion.WithResource("pods")
	//podinformer := infFactory.ForResource(pod)
	podController := initializeController(infFactory, metadataClient, pod, "pod")


	configmap := v1.SchemeGroupVersion.WithResource("configmaps")
	 //configmapinformer := infFactory.ForResource(configmap)
	configmapController := initializeController(infFactory, metadataClient, configmap, "configmap")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt, syscall.SIGTERM)

	go func() {
		infFactory.Start(ctx.Done())

		cacheSynced := cache.WaitForCacheSync(ctx.Done(),
			klusterController.informer.HasSynced,
			podController.informer.HasSynced,
			configmapController.informer.HasSynced,
		)

		if !cacheSynced {
			log.Fatal("failed to sync cache")
		}

		select {
		case <-ctx.Done():
			return
		case <-interrupt:
			cancel() // Cancel the context to stop informers and controllers
		}
	}()


    go klusterController.run(ctx.Done())
	go podController.run(ctx.Done())
	go configmapController.run(ctx.Done())


	<-ctx.Done()

	log.Println("Shutting down...")

	// Stop informers
	infFactory.Shutdown()

	// Add a select statement to block the execution of the main goroutine
	// select{}
	// fmt.Printf("the concrete type that we got is: %v\n", k)

}

func initializeController(infFactory metadatainformer.SharedInformerFactory,metadataClient metadata.Interface,resource schema.GroupVersionResource, resourceName string) *controller {
	resourceInformer := infFactory.ForResource(resource)
	return newController(metadataClient, resourceInformer, resource, resourceName)
}
