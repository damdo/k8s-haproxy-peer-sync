// Note: the example only works with the code within the same release/branch.
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"net"
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
var localHostname string = os.Getenv("HOSTNAME")
var myIPv4Address string = os.Getenv("POD_IP")

type Peer struct {
	addresses []string
	hostname  string
}

func main() {

	// Enable line numbers in logging
	log.SetFlags(log.LstdFlags | log.Lshortfile)

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

	informer := informersdiscoveryv1.NewFilteredEndpointSliceInformer(
		clientset,
		*namespace, 0,
		cache.Indexers{cache.NamespaceIndex: cache.MetaNamespaceIndexFunc},
		listOptionsFunc,
	)
	//informer := factory.Core().V1().Pods().Informer()
	//informer := factory.Discovery().V1().EndpointSlice().Informer()

	myIPv4Address, err = getInterfaceIpv4Addr("eth0")
	if err != nil {
		panic(err.Error())
	}

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
			mOldObj := oldObj.(*discoveryv1.EndpointSlice)
			mObj := obj.(*discoveryv1.EndpointSlice)
			log.Printf("ES updated: '%s'\n", mObj.GetName())

			var oldPeers []Peer
			// we need to always add ourselves in the peerlist
			// even if we are not in the EndpointSlice yet,
			// because that's needed for the HAProxy config to work
			oldPeers = append(oldPeers, Peer{addresses: []string{myIPv4Address}, hostname: localHostname})
			for _, v := range mOldObj.Endpoints {
				//if *v.Conditions.Ready {
				if v.Addresses[0] != myIPv4Address {
					oldPeers = append(oldPeers, Peer{addresses: v.Addresses, hostname: v.TargetRef.Name})
				}
				//}
			}

			var peers []Peer
			// we need to always add ourselves in the peerlist
			// even if we are not in the EndpointSlice yet,
			// because that's needed for the HAProxy config to work
			peers = append(peers, Peer{addresses: []string{myIPv4Address}, hostname: localHostname})
			for _, v := range mObj.Endpoints {
				//if *v.Conditions.Ready {
				if v.Addresses[0] != myIPv4Address {
					peers = append(peers, Peer{addresses: v.Addresses, hostname: v.TargetRef.Name})
				}
				//}
			}

			toRemove := difference(oldPeers, peers)
			log.Printf("ES: '%s', desired: %#v, deletions: %#v\n", mObj.GetName(), peers, toRemove)

			updateHaproxy(peers, toRemove)
		},
	})

	informer.Run(stopper)
}

type Result struct {
	X map[string]interface{} `json:"-"`
}

func getInterfaceIpv4Addr(interfaceName string) (addr string, err error) {
	var (
		ief      *net.Interface
		addrs    []net.Addr
		ipv4Addr net.IP
	)
	if ief, err = net.InterfaceByName(interfaceName); err != nil { // get interface
		return
	}
	if addrs, err = ief.Addrs(); err != nil { // get addresses
		return
	}
	for _, addr := range addrs { // get ipv4 address
		if ipv4Addr = addr.(*net.IPNet).IP.To4(); ipv4Addr != nil {
			break
		}
	}
	if ipv4Addr == nil {
		return "", errors.New(fmt.Sprintf("interface %s don't have an ipv4 address\n", interfaceName))
	}
	return ipv4Addr.String(), nil
}

// difference returns the elements in `a` that aren't in `b`.
func difference(a []Peer, b []Peer) []Peer {
	mb := make(map[string]struct{}, len(b))
	for _, x := range b {
		mb[x.hostname] = struct{}{}
	}
	var diff []Peer
	for _, x := range a {
		if _, found := mb[x.hostname]; !found {
			diff = append(diff, x)
		}
	}
	return diff
}

func updateHaproxy(desired []Peer, deletions []Peer) {
	client := &http.Client{}

	req, err := http.NewRequest("GET", "http://"+*dataPlaneAPIAddress+"/v2/services/haproxy/configuration/version", nil)
	if err != nil {
		log.Println(err)
	}
	req.SetBasicAuth(*user, *password)
	req.Header.Add("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		log.Println(err)
	}

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		panic(err)
	}

	var version int
	if err := json.Unmarshal(body, &version); err != nil {
		panic(err)
	}

	req, err = http.NewRequest("POST", fmt.Sprintf("http://"+*dataPlaneAPIAddress+"/v2/services/haproxy/transactions?version=%d", version), nil)
	if err != nil {
		log.Println(err)
	}
	req.SetBasicAuth(*user, *password)
	req.Header.Add("Content-Type", "application/json")
	resp, err = client.Do(req)
	if err != nil {
		log.Println(err)
	}

	body, err = ioutil.ReadAll(resp.Body)
	if err != nil {
		panic(err)
	}
	resultMap := Result{}
	if err := json.Unmarshal(body, &resultMap.X); err != nil {
		panic(err)
	}
	transactionID := ""
	if n, ok := resultMap.X["id"].(string); ok {
		transactionID = string(n)
	} else {
		panic(err)
	}

	log.Println("transaction: starting transaction against HAProxy DataPlane API")
	log.Printf("transaction: CREATION, version=%d, transaction_id='%s', status_code=%d\n", version, transactionID, resp.StatusCode)

	// create requests
	body = []byte(fmt.Sprintf(`{"name": "%s"}`, *peerSectionName))
	req, err = http.NewRequest("POST", fmt.Sprintf("http://"+*dataPlaneAPIAddress+"/v2/services/haproxy/configuration/peer_section?transaction_id=%s", transactionID), bytes.NewReader(body))
	if err != nil {
		log.Println(err)
	}
	req.SetBasicAuth(*user, *password)
	req.Header.Add("Content-Type", "application/json")
	resp, err = client.Do(req)
	if err != nil {
		log.Println(err)
	}
	log.Printf("peer_section: '%s' CREATION, status_code=%d\n", *peerSectionName, resp.StatusCode)

	for _, p := range desired {

		if p.addresses[0] == myIPv4Address {
			// we don't want to modify the local entry
			// (already present in the runtime config)
			continue
		}

		bodyStr := fmt.Sprintf(`{"name": "%s", "address":"%s", "port":%d}`, p.hostname, p.addresses[0], *peersPort)
		body := []byte(bodyStr)
		req, err := http.NewRequest("POST", "http://"+*dataPlaneAPIAddress+"/v2/services/haproxy/configuration/peer_entries", bytes.NewReader(body))
		if err != nil {
			log.Println(err)
		}
		req.SetBasicAuth(*user, *password)
		q := req.URL.Query()
		q.Add("peer_section", *peerSectionName)
		q.Add("transaction_id", transactionID)
		req.URL.RawQuery = q.Encode()
		req.Header.Add("Content-Type", "application/json")

		resp, err := client.Do(req)
		if err != nil {
			log.Println(err)
		}
		log.Printf("peer_entries: 'peer %s %s:%d' CREATION, status_code=%d\n", p.hostname, p.addresses[0], *peersPort, resp.StatusCode)
	}

	for _, p := range deletions {
		if p.addresses[0] == myIPv4Address {
			// we don't want to modify the local entry
			// (already present in the runtime config)
			continue
		}

		bodyStr := fmt.Sprintf(`{"name": "%s", "address":"%s", "port":%d}`, p.hostname, p.addresses[0], *peersPort)

		body := []byte(bodyStr)
		req, err := http.NewRequest("DELETE", fmt.Sprintf("http://"+*dataPlaneAPIAddress+"/v2/services/haproxy/configuration/peer_entries/%s", p.hostname), bytes.NewReader(body))
		if err != nil {
			log.Println(err)
		}
		req.SetBasicAuth(*user, *password)
		q := req.URL.Query()
		q.Add("peer_section", *peerSectionName)
		q.Add("transaction_id", transactionID)
		req.URL.RawQuery = q.Encode()
		req.Header.Add("Content-Type", "application/json")

		resp, err := client.Do(req)
		if err != nil {
			log.Println(err)
		}
		log.Printf("peer_entries: 'peer %s %s:%d' DELETION, status_code=%d\n", p.hostname, p.addresses[0], *peersPort, resp.StatusCode)
	}

	// commit: /services/haproxy/transactions/{id}
	req, err = http.NewRequest("PUT", fmt.Sprintf("http://"+*dataPlaneAPIAddress+"/v2/services/haproxy/transactions/%s", transactionID), nil)
	if err != nil {
		log.Println(err)
	}
	req.SetBasicAuth(*user, *password)
	req.Header.Add("Content-Type", "application/json")
	resp, err = client.Do(req)
	if err != nil {
		log.Println(err)
	}
	body, err = ioutil.ReadAll(resp.Body)
	if err != nil {
		panic(err)
	}
	resultMap = Result{}
	if err := json.Unmarshal(body, &resultMap.X); err != nil {
		panic(err)
	}
	log.Printf("transaction: COMMIT transaction_id='%s', status_code=%d, result=%#v\n", transactionID, resp.StatusCode, resultMap.X)
}
