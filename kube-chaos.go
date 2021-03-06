/*
Copyright 2016 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

// Note: the example only works with the code within the same release/branch.
package main

import (
	"flag"
	"fmt"
	"regexp"
	"time"

	"github.com/golang/glog"
	"github.com/huanwei/kube-chaos/pkg/exec"
	"github.com/huanwei/kube-chaos/pkg/flow"
	meta_v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

func main() {
	var (
		kubeconfig    string
		endpoint      string
		labelSelector string
		syncDuration  int
	)
	flag.StringVar(&kubeconfig, "kubeconfig", "/etc/kubernetes/kubelet.conf", "absolute path to the kubeconfig file")
	flag.StringVar(&endpoint, "etcd-endpoint", "", "the calico etcd endpoint, e.g. http://10.96.232.136:6666")
	flag.StringVar(&labelSelector, "labelSelector", "", "select pods to do chaos, e.g. chaos=on")
	flag.IntVar(&syncDuration, "syncDuration", 10, "sync duration(seconds)")
	flag.Parse()
	// uses the current context in kubeconfig
	config, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	fmt.Println(err)
	if err != nil {
		panic(err.Error())
	}
	// creates the clientset
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		panic(err.Error())
	}
	// init ifb module
	err = flow.InitIfbModule()
	if err != nil {
		glog.Errorf("Failed init ifb: %v", err)
	}
	//Synchronize pods and do chaos
	for {
		//pods, err := clientset.CoreV1().Pods("").List(meta_v1.ListOptions{FieldSelector: "spec.nodeName=10.10.103.182", LabelSelector: labelSelector})
		pods, err := clientset.CoreV1().Pods("").List(meta_v1.ListOptions{LabelSelector: labelSelector})
		if err != nil {
			glog.Errorf("Failed list pods: %v", err)
		}
		glog.V(4).Infof("There are %d pods need to do chaos in the cluster\n", len(pods.Items))

		//used for  checking which tc class isn't used, and del it
		egressPodsCIDRs := []string{}
		ingressPodsCIDRs := []string{}
		for _, pod := range pods.Items {

			// todo - fix

			ingressChaosInfo, egressChaosInfo, err := flow.ExtractPodChaosInfo(pod.Annotations)
			if err != nil {
				glog.Errorf("Failed extract pod's chaos info: %v", err)
			}
			if ingressChaosInfo == "" && egressChaosInfo == "" {
				glog.Warning("chaos is on, but the pod's chaos info was not set")
				continue
			}

			cidr := fmt.Sprintf("%s/32", pod.Status.PodIP) //192.168.0.10/32
			if egressChaosInfo != "" {
				egressPodsCIDRs = append(egressPodsCIDRs, cidr)
			}
			if ingressChaosInfo != "" {
				ingressPodsCIDRs = append(ingressPodsCIDRs, cidr)
			}

			//fetch pod's vethname from calico's etcd
			e := exec.New()
			//data, err := e.Command("etcdctl", "--endpoint=http://10.96.232.136:6666", "get", "/calico/v1/host/"+pod.Status.HostIP+"/workload/k8s/"+pod.Namespace+"."+pod.Name+"/endpoint/eth0").CombinedOutput()

			//curl -L 10.10.102.80:2379/v2/keys/calico/v1/host/10.10.102.80/workload/k8s/kube-system.nfs-controller-d6dw8/endpoint/eth0
			data, err := e.Command("curl", "-L", endpoint+"/v2/keys/calico/v1/host/"+pod.Status.HostIP+"/workload/k8s/"+pod.Namespace+"."+pod.Name+"/endpoint/eth0").CombinedOutput()
			if err != nil {
				glog.Errorf("Failed fetch pod %s interface name: %v", pod.Name, err)
			}
			//get the pod's calico vethname
			re, _ := regexp.Compile("cali[a-f0-9]{11}")
			vethName := string(re.Find(data)) //cali67801d38217
			glog.V(4).Infof("pod %s's vethname is %s", pod.Name, vethName)

			//todo - fix
			shaper := flow.NewTCShaper(vethName)
			//config pod interface  qdisc, and mirror to ifb
			if err := shaper.ReconcileInterface(egressChaosInfo, ingressChaosInfo); err != nil {
				glog.Errorf("Failed to init veth(%s): %v", vethName, err)
			}

			if err := shaper.ReconcileCIDR(cidr, egressChaosInfo, ingressChaosInfo); err != nil {
				glog.Errorf("Failed to reconcile CIDR %s: %v", cidr, err)
			}
			glog.V(4).Infof("reconcile cidr %s with egressChaosInfo %s and ingressChaosInfo %s ", cidr, egressChaosInfo, ingressChaosInfo)

		}
		if err := flow.DeleteExtraChaos(egressPodsCIDRs, ingressPodsCIDRs); err != nil {
			glog.Errorf("Failed to delete extra chaos: %v", err)
		}
		time.Sleep(time.Duration(syncDuration) * time.Second)
	}

}
