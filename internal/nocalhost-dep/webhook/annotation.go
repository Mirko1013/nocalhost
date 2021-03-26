package webhook

import (
	"context"
	"github.com/golang/glog"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"nocalhost/internal/nhctl/app"
	"time"
)

type ObjectMetaHolder struct {
	// Standard object's metadata.
	// More info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty" protobuf:"bytes,1,opt,name=metadata"`
}

func (o *ObjectMetaHolder) getOwnRefSignedAnnotation(ns string) []string {
	// resolve object meta
	glog.Infof("omh: %+v", o)
	if len(o.OwnerReferences) > 0 {

		config, err := rest.InClusterConfig()
		if err != nil {
			glog.Error(err)
			return nil
		}
		// creates the clientset
		client, err := dynamic.NewForConfig(config)
		if err != nil {
			glog.Error(err)
			return nil
		}

		dataCh := make(chan []string, 1)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)

		go func() {
			for _, reference := range o.OwnerReferences {
				gv, err := schema.ParseGroupVersion(reference.APIVersion)
				if err != nil {
					glog.Infof("Can't not parse gv by apiVersion (%s): %v", reference.APIVersion, err)
					continue
				}

				mapping, err := cachedRestMapper.RESTMapping(schema.GroupKind{
					Group: gv.Group,
					Kind:  reference.Kind,
				}, gv.Version)
				if err != nil {
					glog.Infof("Fail to find gvr by gvk g(%s) v(%s) k(%s): %v", gv.Group, gv.Version, reference.Kind, err)
					continue
				}
				if mapping == nil {
					glog.Infof("Can't not find gvr by gvk g(%s) v(%s) k(%s)", gv.Group, gv.Version, reference.Kind)
					continue
				}

				name := reference.Name

				go func() {
					resource, err := client.Resource(mapping.Resource).Namespace("").Get(ctx, name, metav1.GetOptions{})
					if err == nil && resource != nil {
						if pair := containsAnnotationSign(resource.GetAnnotations()); len(pair) > 0 {
							dataCh <- pair
						}
					} else {
						glog.Infof("Fail to find by gvr(%v) with name(%s) ns(%s): %v", mapping.Resource, name, "", err)
					}
				}()

				go func() {
					resource, err := client.Resource(mapping.Resource).Namespace(ns).Get(ctx, name, metav1.GetOptions{})
					if err == nil && resource != nil {
						if pair := containsAnnotationSign(resource.GetAnnotations()); len(pair) > 0 {
							dataCh <- pair
						}
					} else {
						glog.Infof("Fail to find by gvr(%v) with name(%s) ns(%s): %v", mapping.Resource.Resource, name, ns, err)
					}
				}()
			}
		}()

		select {
		case group := <-dataCh:
			cancel()
			return group
		case <-ctx.Done():
			glog.Infof("timeout while getting owner ref")
		}
	}

	return nil
}

func containsAnnotationSign(annos map[string]string) []string {
	for k, desiredVal := range annos {
		glog.Infof("anno key: %s", k)
		if k == app.NocalhostApplicationName || k == app.HelmReleaseName {
			return []string{k, desiredVal}
		}
	}
	return nil
}
