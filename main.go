package main

import (
	"flag"
	"fmt"
	"log"
	"path/filepath"
	"time"

	"github.com/VedRatan/kluster/pkg/apis/vedratan.dev/v1alpha1"


	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/dynamic/dynamicinformer"
	"k8s.io/client-go/rest"
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

	dynamicClient, err := dynamic.NewForConfig(config)

	if err != nil{
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

	infFactory := dynamicinformer.NewDynamicSharedInformerFactory(dynamicClient, 10*time.Minute)

	c := newController(dynamicClient, infFactory)
	infFactory.Start(make(<-chan struct{}))
	c.run(make(<-chan struct{}))
	fmt.Printf("the concrete type that we got is: %v\n", k)

}