package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"time"

	discoveryv1 "k8s.io/api/discovery/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	informersdiscoveryv1 "k8s.io/client-go/informers/discovery/v1"
	"k8s.io/client-go/informers/internalinterfaces"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	klog "k8s.io/klog/v2"
)

var (
	service             = flag.String("service", "", "the Kubernetes Service that looks after the HAProxy pods")
	namespace           = flag.String("namespace", "", "the Kubernetes Namespace where the HAProxy setup lives")
	user                = flag.String("user", "admin", "the username to access the DataPlane API via HTTP Basic Access Authentication")
	password            = flag.String("password", "", "the username to access the DataPlane API via HTTP Basic Access Authentication")
	dataPlaneAPIAddress = flag.String("data-plane-api-address", "127.0.0.1:5555", "(optional) the address (ip:port) where the HAProxy DataPlane API is listening")
	peerSectionName     = flag.String("peer-section-name", "haproxy-peers", "(optional) the name of the peer-section to sync")
	networkInterface    = flag.String("network-interface", "eth0", "(optional) the network interface that HAProxy uses for peer communication")
	startupDelayStr     = flag.String("startup-delay", "2s", "(optional) initial delay to wait for HAProxy DataPlane API to be up and running")
	peersPort           = flag.Int("peer-port", 3000, "(optional) the port where HAProxy listens for peer communication")
	ownIPAddress        string
)

func main() {
	flag.Parse()

	if *service == "" || *namespace == "" || *password == "" {
		flag.Usage()
		klog.Fatalln("error missing non optional flags")
	}
	startupDelay, err := time.ParseDuration(*startupDelayStr)
	if err != nil {
		klog.Fatalln(err)
	}

	klog.Info("Starting with config:")
	klog.Infof("service=%#v\n", *service)
	klog.Infof("namespace=%#v\n", *namespace)
	klog.Infof("user=%#v\n", *user)
	klog.Info("password=<REDACTED>")
	klog.Infof("dataPlaneAPIAddress=%#v\n", *dataPlaneAPIAddress)
	klog.Infof("peerSectionName=%#v\n", *peerSectionName)
	klog.Infof("peersPort=%#v\n", *peersPort)
	klog.Infof("networkInterface=%#v\n", *networkInterface)
	klog.Infof("startupDelay=%#v\n", *networkInterface)

	// create a context.Context that is cancelled on an os.Interrupt signal. This allows to prevent the application
	// from exiting until it receives an interrupt signal. Its 'ctx.Done()' channel is passed to informer.Run, to keep the informer
	// alive until execution is cancelled.
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	klog.Infof("waiting for %s startup delay", *startupDelayStr)

	select {
	case <-time.After(startupDelay):
		// delay for startupDelay amount
	case <-ctx.Done():
		os.Exit(0)
	}

	// get own IPv4 address
	ownIPAddress, err := getInterfaceIpv4Addr(*networkInterface)
	if err != nil {
		panic(err.Error())
	}

	// in-cluster config
	config, err := rest.InClusterConfig()
	if err != nil {
		panic(err.Error())
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

	informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			mObj := obj.(*discoveryv1.EndpointSlice)
			klog.Infof("ES added: %s", mObj.GetName())
		},
		DeleteFunc: func(obj interface{}) {
			mObj := obj.(*discoveryv1.EndpointSlice)
			klog.Infof("ES deleted: %s", mObj.GetName())
		},
		UpdateFunc: func(oldObj, obj interface{}) {
			mOldObj := oldObj.(*discoveryv1.EndpointSlice)
			mObj := obj.(*discoveryv1.EndpointSlice)
			klog.Infof("ES updated: '%s'\n", mObj.GetName())

			var oldPeers []Peer
			for _, v := range mOldObj.Endpoints {
				if v.Addresses[0] != ownIPAddress {
					oldPeers = append(oldPeers, Peer{addresses: v.Addresses, hostname: v.TargetRef.Name})
				}
			}

			var desiredPeers []Peer
			for _, v := range mObj.Endpoints {
				if v.Addresses[0] != ownIPAddress {
					desiredPeers = append(desiredPeers, Peer{addresses: v.Addresses, hostname: v.TargetRef.Name})
				}
			}

			toRemove := difference(oldPeers, desiredPeers)
			klog.Infof("ES: '%s', desired: %#v, toRemove: %#v\n", mObj.GetName(), desiredPeers, toRemove)

			updatePeers(ctx, desiredPeers, toRemove)
		},
	})

	informer.Run(ctx.Done())
}

var res map[string]interface{}

func updatePeers(ctx context.Context, desired []Peer, deletions []Peer) {
	client := &http.Client{}

	// get current HAProxy config version
	// https://www.haproxy.com/documentation/dataplaneapi/community/#get-/services/haproxy/configuration/version
	req, err := http.NewRequestWithContext(ctx, "GET", "http://"+*dataPlaneAPIAddress+"/v2/services/haproxy/configuration/version", nil)
	if err != nil {
		panic(err)
	}
	req.SetBasicAuth(*user, *password)
	req.Header.Add("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		panic(err)
	}
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		panic(err)
	}
	var version int
	if err := json.Unmarshal(body, &version); err != nil {
		panic(err)
	}

	// start a transaction against the HAProxy DataPlane API for the current version
	// https://www.haproxy.com/documentation/dataplaneapi/community/#post-/services/haproxy/transactions
	req, err = http.NewRequestWithContext(ctx, "POST", "http://"+*dataPlaneAPIAddress+"/v2/services/haproxy/transactions", nil)
	if err != nil {
		panic(err)
	}
	q := req.URL.Query()
	q.Add("version", strconv.Itoa(version))
	req.URL.RawQuery = q.Encode()
	req.SetBasicAuth(*user, *password)
	req.Header.Add("Content-Type", "application/json")

	resp, err = client.Do(req)
	if err != nil {
		panic(err)
	}
	body, err = ioutil.ReadAll(resp.Body)
	if err != nil {
		panic(err)
	}
	resultMap := make(map[string]interface{})

	if err := json.Unmarshal(body, &resultMap); err != nil {
		panic(err)
	}
	transactionID := ""
	if n, ok := resultMap["id"].(string); ok {
		transactionID = string(n)
	} else {
		panic(err)
	}
	klog.Info("transaction: starting transaction against HAProxy DataPlane API")
	klog.Infof("transaction: CREATION, version=%d, transaction_id='%s', status_code=%d\n", version, transactionID, resp.StatusCode)

	// idempotently add a new peer_section to the HAProxy config
	// https://www.haproxy.com/documentation/dataplaneapi/community/#post-/services/haproxy/configuration/peer_section
	body = []byte(fmt.Sprintf(`{"name": "%s"}`, *peerSectionName))
	req, err = http.NewRequestWithContext(ctx, "POST", "http://"+*dataPlaneAPIAddress+"/v2/services/haproxy/configuration/peer_section", bytes.NewReader(body))
	if err != nil {
		panic(err)
	}
	q = req.URL.Query()
	q.Add("transaction_id", transactionID)
	req.URL.RawQuery = q.Encode()
	req.SetBasicAuth(*user, *password)
	req.Header.Add("Content-Type", "application/json")

	resp, err = client.Do(req)
	if err != nil {
		panic(err)
	}
	klog.Infof("peer_section: '%s' CREATION, status_code=%d\n", *peerSectionName, resp.StatusCode)

	// idempotently add the desired `peer_entry`s to the previously created peer_section in the HAProxy config
	for _, p := range desired {
		if p.addresses[0] == ownIPAddress {
			// we don't want to modify the local entry
			// (already present in the runtime config)
			continue
		}

		// https://www.haproxy.com/documentation/dataplaneapi/community/#post-/services/haproxy/configuration/peer_entries
		body := []byte(fmt.Sprintf(`{"name": "%s", "address":"%s", "port":%d}`, p.hostname, p.addresses[0], *peersPort))
		req, err := http.NewRequestWithContext(ctx, "POST", "http://"+*dataPlaneAPIAddress+"/v2/services/haproxy/configuration/peer_entries", bytes.NewReader(body))
		if err != nil {
			panic(err)
		}
		q := req.URL.Query()
		q.Add("peer_section", *peerSectionName)
		q.Add("transaction_id", transactionID)
		req.URL.RawQuery = q.Encode()
		req.SetBasicAuth(*user, *password)
		req.Header.Add("Content-Type", "application/json")

		resp, err := client.Do(req)
		if err != nil {
			panic(err)
		}
		klog.Infof("peer_entries: 'peer %s %s:%d' CREATION, status_code=%d\n", p.hostname, p.addresses[0], *peersPort, resp.StatusCode)
	}

	// idempotently delete the unneded `peer_entry`s from the previously created peer_section in the HAProxy config
	for _, p := range deletions {
		if p.addresses[0] == ownIPAddress {
			// we don't want to modify the local entry
			// (already present in the runtime config)
			continue
		}

		// https://www.haproxy.com/documentation/dataplaneapi/community/#delete-/services/haproxy/configuration/peer_entries/-name-
		body := []byte(fmt.Sprintf(`{"name": "%s", "address":"%s", "port":%d}`, p.hostname, p.addresses[0], *peersPort))
		req, err := http.NewRequestWithContext(ctx, "DELETE", fmt.Sprintf("http://"+*dataPlaneAPIAddress+"/v2/services/haproxy/configuration/peer_entries/%s", p.hostname), bytes.NewReader(body))
		if err != nil {
			panic(err)
		}
		req.SetBasicAuth(*user, *password)
		q := req.URL.Query()
		q.Add("peer_section", *peerSectionName)
		q.Add("transaction_id", transactionID)
		req.URL.RawQuery = q.Encode()
		req.Header.Add("Content-Type", "application/json")

		resp, err := client.Do(req)
		if err != nil {
			panic(err)
		}
		klog.Infof("peer_entries: 'peer %s %s:%d' DELETION, status_code=%d\n", p.hostname, p.addresses[0], *peersPort, resp.StatusCode)
	}

	// commit the previously started transaction against the HAProxy DataPlane API
	// https://www.haproxy.com/documentation/dataplaneapi/community/#put-/services/haproxy/transactions/-id-
	req, err = http.NewRequestWithContext(ctx, "PUT", fmt.Sprintf("http://"+*dataPlaneAPIAddress+"/v2/services/haproxy/transactions/%s", transactionID), nil)
	if err != nil {
		panic(err)
	}
	req.SetBasicAuth(*user, *password)
	req.Header.Add("Content-Type", "application/json")

	resp, err = client.Do(req)
	if err != nil {
		panic(err)
	}
	body, err = ioutil.ReadAll(resp.Body)
	if err != nil {
		panic(err)
	}
	resultMap = make(map[string]interface{})
	if err := json.Unmarshal(body, &resultMap); err != nil {
		panic(err)
	}
	klog.Infof("transaction: COMMIT transaction_id='%s', status_code=%d, result=%#v\n", transactionID, resp.StatusCode, resultMap)
}
