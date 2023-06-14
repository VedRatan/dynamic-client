package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"path/filepath"
	"time"

	"github.com/VedRatan/kluster/pkg/apis/vedratan.dev/v1alpha1"

	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/metadata"
	"k8s.io/client-go/metadata/metadatainformer"

	// "k8s.io/client-go/dynamic"
	// "k8s.io/client-go/dynamic/dynamicinformer"
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

	// unsObject, err:= dynamicClient.Resource(schema.GroupVersionResource{
	// 	Group: "vedratan.dev",
	// 	Version: "v1alpha1",
	// 	Resource: "klusters",
	// }).Namespace("default").Get(context.Background(), "kluster-0", v1.GetOptions{})

	// if err != nil {
	// 	fmt.Printf("error %s getting resource from dynamic client\n", err.Error())
	// }

	k := v1alpha1.Kluster{}
	// getting and setting fields on unsObject
	//fmt.Printf("Go teh object %s\n", unsObject.GetName())

	// convert unsObject into a typed object
	// err = runtime.DefaultUnstructuredConverter.FromUnstructured(unsObject.UnstructuredContent(), &k)
	// if err != nil {
	// 	fmt.Printf("error %s, converting unstructured to kluster type", err.Error())
	// }

	infFactory := metadatainformer.NewFilteredSharedInformerFactory(metadataClient, 10*time.Minute, "", func(options *v1.ListOptions) {
		options.LabelSelector = "foo"
	})
	resource := v1alpha1.SchemeGroupVersion.WithResource("klusters")
	fmt.Printf("%+v\n", resource)
	c := newController(metadataClient, infFactory, resource)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	infFactory.Start(ctx.Done())

	if !cache.WaitForCacheSync(ctx.Done(), c.informer.HasSynced) {
		fmt.Print("waiting for the cache to be synced...\n")
	}

	c.run(ctx.Done())
	fmt.Printf("the concrete type that we got is: %v\n", k)

}
