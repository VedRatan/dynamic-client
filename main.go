package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"path/filepath"
	"time"

	"github.com/VedRatan/kluster/pkg/apis/vedratan.dev/v1alpha1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
    "k8s.io/api/core/v1"
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


	k := v1alpha1.Kluster{}

	infFactory := metadatainformer.NewFilteredSharedInformerFactory(metadataClient, 10*time.Minute, "", func(options *metav1.ListOptions) {
		options.LabelSelector = "ttl"
	})
	kluster := v1alpha1.SchemeGroupVersion.WithResource("klusters")
	pod := v1.SchemeGroupVersion.WithResource("pods")
	configmap := v1.SchemeGroupVersion.WithResource("configmaps")
	fmt.Printf("%+v\n", kluster)
	fmt.Printf("%+v\n", pod)
	fmt.Printf("%+v\n", configmap)
	klusterinformer := infFactory.ForResource(kluster)
	podinformer := infFactory.ForResource(pod)
	configmapinformer := infFactory.ForResource(configmap)
	c := newController(metadataClient, klusterinformer, podinformer, configmapinformer)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	infFactory.Start(ctx.Done())

	if !cache.WaitForCacheSync(ctx.Done(), c.klusterinformer.HasSynced) {
		fmt.Print("waiting for the cache to be synced...\n")
	}

	if !cache.WaitForCacheSync(ctx.Done(), c.podinformer.HasSynced) {
		fmt.Print("waiting for the cache to be synced...\n")
	}

	if !cache.WaitForCacheSync(ctx.Done(), c.cminformer.HasSynced) {
		fmt.Print("waiting for the cache to be synced...\n")
	}

	c.run(ctx.Done())
	fmt.Printf("the concrete type that we got is: %v\n", k)

}
