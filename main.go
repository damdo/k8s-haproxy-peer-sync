// Note: the example only works with the code within the same release/branch.
package main

import (
	"flag"
	"log"
	"path/filepath"

	discoveryv1 "k8s.io/api/discovery/v1"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/homedir"
	//
	// Uncomment to load all auth plugins
	// _ "k8s.io/client-go/plugin/pkg/client/auth"
	//
	// Or uncomment to load specific auth plugins
	// _ "k8s.io/client-go/plugin/pkg/client/auth/azure"
	// _ "k8s.io/client-go/plugin/pkg/client/auth/gcp"
	// _ "k8s.io/client-go/plugin/pkg/client/auth/oidc"
	// _ "k8s.io/client-go/plugin/pkg/client/auth/openstack"
)

func main() {
	//// in-cluster config
	//config, err := rest.InClusterConfig()
	//if err != nil {
	//	panic(err.Error())
	//}

	var kubeconfig *string
	if home := homedir.HomeDir(); home != "" {
		kubeconfig = flag.String("kubeconfig", filepath.Join(home, ".kube", "config"), "(optional) absolute path to the kubeconfig file")
	} else {
		kubeconfig = flag.String("kubeconfig", "", "absolute path to the kubeconfig file")
	}
	flag.Parse()

	// use the current context in kubeconfig
	config, err := clientcmd.BuildConfigFromFlags("", *kubeconfig)
	if err != nil {
		panic(err.Error())
	}

	// create the clientset
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		panic(err.Error())
	}

	factory := informers.NewSharedInformerFactory(clientset, 0)
	//informer := factory.Core().V1().Pods().Informer()
	informer := factory.Discovery().V1().EndpointSlices().Informer()
	stopper := make(chan struct{})
	defer close(stopper)
	informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		//AddFunc: func(obj interface{}) {
		//	mObj := obj.(v1.Object)
		//	log.Printf("ES added: %s", mObj.GetName())
		//},
		//DeleteFunc: func(obj interface{}) {
		//	mObj := obj.(v1.Object)
		//	log.Printf("ES deleted: %s", mObj.GetName())
		//},
		UpdateFunc: func(oldObj, obj interface{}) {
			mObj := obj.(*discoveryv1.EndpointSlice)
			log.Printf("ES updated: %s", mObj.GetName())
		},
	})

	informer.Run(stopper)
}
