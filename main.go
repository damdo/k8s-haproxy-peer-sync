// Note: the example only works with the code within the same release/branch.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"

	discoveryv1 "k8s.io/api/discovery/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	informersdiscoveryv1 "k8s.io/client-go/informers/discovery/v1"
	"k8s.io/client-go/informers/internalinterfaces"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
)

var service = flag.String("service", "", "")
var namespace = flag.String("namespace", "", "")
var user = flag.String("user", "", "")
var password = flag.String("password", "", "")
var dataPlaneAPIAddress = flag.String("data-plane-api-address", "127.0.0.1:5555", "")
var peerSectionName = flag.String("peer-section-name", "haproxy-peers", "")
var peersPort = flag.Int("peer-port", 3000, "")
var hostname string = os.Getenv("HOSTNAME")

func main() {

	// in-cluster config
	config, err := rest.InClusterConfig()
	if err != nil {
		panic(err.Error())
	}

	//var kubeconfig *string
	//if home := homedir.HomeDir(); home != "" {
	//	kubeconfig = flag.String("kubeconfig", filepath.Join(home, ".kube", "config"), "(optional) absolute path to the kubeconfig file")
	//} else {
	//	kubeconfig = flag.String("kubeconfig", "", "absolute path to the kubeconfig file")
	//}
	//// use the current context in kubeconfig
	//config, err := clientcmd.BuildConfigFromFlags("", *kubeconfig)
	//if err != nil {
	//	panic(err.Error())
	//}

	flag.Parse()

	if *service == "" || *namespace == "" {
		flag.Usage()
		os.Exit(1)
	}

	// create the clientset
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		panic(err.Error())
	}

	listOptionsFunc := internalinterfaces.TweakListOptionsFunc(func(options *metav1.ListOptions) {
		*options = metav1.ListOptions{LabelSelector: "kubernetes.io/service-name=" + *service}
	})

	informer := informersdiscoveryv1.NewFilteredEndpointSliceInformer(clientset, *namespace, 0, cache.Indexers{cache.NamespaceIndex: cache.MetaNamespaceIndexFunc}, listOptionsFunc)
	//informer := factory.Core().V1().Pods().Informer()
	//informer := factory.Discovery().V1().EndpointSlice().Informer()

	stopper := make(chan struct{})
	defer close(stopper)
	informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			mObj := obj.(*discoveryv1.EndpointSlice)
			log.Printf("ES added: %s", mObj.GetName())
		},
		DeleteFunc: func(obj interface{}) {
			mObj := obj.(*discoveryv1.EndpointSlice)
			log.Printf("ES deleted: %s", mObj.GetName())
		},
		UpdateFunc: func(oldObj, obj interface{}) {
			mObj := obj.(*discoveryv1.EndpointSlice)

			var addrs []string
			for _, v := range mObj.Endpoints {
				addrs = append(addrs, v.Addresses[0])
			}
			log.Printf("ES updated: %s %#v", mObj.GetName(), addrs)
			updateHaproxy(addrs)
		},
	})

	informer.Run(stopper)
}

func updateHaproxy(addrs []string) {
	client := &http.Client{}

	body := []byte(fmt.Sprintf(`{"name": "%s"}`, *peerSectionName))
	req, err := http.NewRequest("POST", "http://"+*dataPlaneAPIAddress+"/services/haproxy/configuration/peer_section", bytes.NewReader(body))
	if err != nil {
		log.Println(err)
	}
	req.SetBasicAuth(*user, *password)
	resp, err := client.Do(req)
	if err != nil {
		log.Println(err)
	}
	log.Printf("peer_section: %s creation, got %d status code\n", *peerSectionName, resp.StatusCode)

	for _, addr := range addrs {
		body := []byte(fmt.Sprintf(`{"name": "%s", "address":"%s", "port":%d}`, hostname, addr, *peersPort))
		req, err := http.NewRequest("POST", "http://"+*dataPlaneAPIAddress+"/services/haproxy/configuration/peer_entries", bytes.NewReader(body))
		if err != nil {
			log.Println(err)
		}
		req.SetBasicAuth(*user, *password)
		q := req.URL.Query()
		q.Add("peer_section", *peerSectionName)
		req.URL.RawQuery = q.Encode()

		resp, err := client.Do(req)
		if err != nil {
			log.Println(err)
		}
		log.Printf("peer_entries: (%s:%d) creation, got %d status code\n", addr, *peersPort, resp.StatusCode)
	}
}
