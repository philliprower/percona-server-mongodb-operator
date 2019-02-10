package psmdb

import (
	"context"
	"fmt"
	"strconv"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	api "github.com/Percona-Lab/percona-server-mongodb-operator/pkg/apis/psmdb/v1alpha1"
)

// Service returns a core/v1 API Service
func Service(m *api.PerconaServerMongoDB, replset *api.ReplsetSpec) *corev1.Service {
	ls := map[string]string{
		"app":                       "percona-server-mongodb",
		"percona-server-mongodb_cr": m.Name,
		"replset":                   replset.Name,
	}

	return &corev1.Service{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "Service",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      m.Name + "-" + replset.Name,
			Namespace: m.Namespace,
		},
		Spec: corev1.ServiceSpec{
			Ports: []corev1.ServicePort{
				{
					Name:       mongodPortName,
					Port:       m.Spec.Mongod.Net.Port,
					TargetPort: intstr.FromInt(int(m.Spec.Mongod.Net.Port)),
				},
			},
			ClusterIP: "None",
			Selector:  ls,
		},
	}
}

// ExternalService returns a Service object needs to serve external connections
func ExternalService(m *api.PerconaServerMongoDB, replset *api.ReplsetSpec, podName string) *corev1.Service {
	svc := &corev1.Service{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Service",
			APIVersion: "v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      podName,
			Namespace: m.Namespace,
		},
	}

	svc.Labels = map[string]string{
		"app":     "percona-server-mongodb",
		"type":    "expose-externally",
		"replset": replset.Name,
		"cluster": m.Name,
	}

	svc.Spec = corev1.ServiceSpec{
		Ports: []corev1.ServicePort{
			{
				Name:       mongodPortName,
				Port:       m.Spec.Mongod.Net.Port,
				TargetPort: intstr.FromInt(int(m.Spec.Mongod.Net.Port)),
			},
		},
		Selector: map[string]string{"statefulset.kubernetes.io/pod-name": podName},
	}

	switch replset.Expose.ExposeType {
	case corev1.ServiceTypeNodePort:
		svc.Spec.Type = corev1.ServiceTypeNodePort
		svc.Spec.ExternalTrafficPolicy = "Local"
	case corev1.ServiceTypeLoadBalancer:
		svc.Spec.Type = corev1.ServiceTypeLoadBalancer
		svc.Spec.ExternalTrafficPolicy = "Local"
		svc.Annotations = map[string]string{"service.beta.kubernetes.io/aws-load-balancer-backend-protocol": "tcp"}
	default:
		svc.Spec.Type = corev1.ServiceTypeClusterIP
	}

	return svc
}

type ServiceAddr struct {
	Host string
	Port int
}

func (s ServiceAddr) String() string {
	return s.Host + ":" + strconv.Itoa(s.Port)
}

func GetServiceAddr(svc corev1.Service, pod corev1.Pod, cl client.Client) (*ServiceAddr, error) {
	addr := &ServiceAddr{}

	switch svc.Spec.Type {
	case corev1.ServiceTypeClusterIP:
		addr.Host = svc.Spec.ClusterIP
		for _, p := range svc.Spec.Ports {
			if p.Name != mongodPortName {
				continue
			}
			addr.Port = int(p.Port)
		}

	case corev1.ServiceTypeLoadBalancer:
		host, err := getIngressPoint(pod, cl)
		if err != nil {
			return nil, err
		}
		addr.Host = host
		for _, p := range svc.Spec.Ports {
			if p.Name != mongodPortName {
				continue
			}
			addr.Port = int(p.Port)
		}

	case corev1.ServiceTypeNodePort:
		addr.Host = pod.Status.HostIP
		for _, p := range svc.Spec.Ports {
			if p.Name != mongodPortName {
				continue
			}
			addr.Port = int(p.NodePort)
		}
	}
	return addr, nil
}

func getIngressPoint(pod corev1.Pod, cl client.Client) (string, error) {
	var retries uint64 = 0

	meta := &corev1.Service{}

	ticker := time.NewTicker(1 * time.Second)

	for range ticker.C {

		if retries >= 900 {
			ticker.Stop()
			return "", fmt.Errorf("failed to fetch service. Retries limit reached")
		}

		err := cl.Get(context.TODO(), types.NamespacedName{Name: pod.Name, Namespace: pod.Namespace}, meta)

		if err != nil {
			ticker.Stop()
			return "", fmt.Errorf("failed to fetch service: %v", err)
		}

		if len(meta.Status.LoadBalancer.Ingress) != 0 {
			ticker.Stop()
		}
		retries++
	}

	if len(meta.Status.LoadBalancer.Ingress) == 0 {
		return "", fmt.Errorf("cannot detect ingress point for Service %s", meta.Name)
	}

	ip := meta.Status.LoadBalancer.Ingress[0].IP
	hostname := meta.Status.LoadBalancer.Ingress[0].Hostname

	if ip == "" && hostname == "" {
		return "", fmt.Errorf("cannot fetch any hostname from ingress for Service %s", meta.Name)
	}
	if ip != "" {
		return ip, nil
	}
	return hostname, nil
}