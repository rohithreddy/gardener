// Copyright (c) 2019 SAP SE or an SAP affiliate company. All rights reserved. This file is licensed under the Apache Software License, v. 2 except as noted otherwise in the LICENSE file
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package chart

import (
	"context"

	gardenv1beta1 "github.com/gardener/gardener/pkg/apis/garden/v1beta1"
	gardenerkubernetes "github.com/gardener/gardener/pkg/client/kubernetes"
	"github.com/gardener/gardener/pkg/utils/imagevector"

	"github.com/pkg/errors"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Chart represents a Helm chart (and its sub-charts) that can be applied and deleted.
type Chart struct {
	Name       string
	Path       string
	Images     []string
	ValuesFunc func(string, *gardenv1beta1.Shoot, map[string]string) (map[string]interface{}, error)
	Objects    []*Object
	SubCharts  []*Chart
}

// Object represents an object deployed by a Chart.
type Object struct {
	Type runtime.Object
	Name string
}

// Apply applies this chart into the given namespace using the given chartApplier. Before applying the chart,
// it collects its values, starting with values returned by ValuesFunc, and injecting images, subchart values,
// and provider values as needed.
func (c *Chart) Apply(
	ctx context.Context,
	k8sClient gardenerkubernetes.Interface,
	chartApplier gardenerkubernetes.ChartApplier,
	namespace string,
	shoot *gardenv1beta1.Shoot,
	imageVector imagevector.ImageVector,
	checksums map[string]string,
	additionalValues map[string]interface{},
) error {

	// Get chart values
	values, err := c.getValues(namespace, k8sClient.Version(), shoot, imageVector, checksums)
	if err != nil {
		return err
	}

	// Apply chart
	err = chartApplier.ApplyChart(ctx, c.Path, namespace, c.Name, values, additionalValues)
	if err != nil {
		return errors.Wrapf(err, "could not apply chart '%s' in namespace '%s'", c.Name, namespace)
	}
	return nil
}

// getValues collects and returns this chart's values, starting with values returned by ValuesFunc, and injecting
// images and subchart values as needed.
func (c *Chart) getValues(
	clusterName string,
	k8sVersion string,
	shoot *gardenv1beta1.Shoot,
	imageVector imagevector.ImageVector,
	checksums map[string]string,
) (map[string]interface{}, error) {

	// Get default values
	values := make(map[string]interface{})
	var err error
	if c.ValuesFunc != nil {
		values, err = c.ValuesFunc(clusterName, shoot, checksums)
		if err != nil {
			return nil, errors.Wrapf(err, "could not get chart '%s' default values for cluster '%s'", c.Name, clusterName)
		}
	}

	// Inject images
	if len(c.Images) > 0 {
		values, err = imageVector.InjectImages(values, k8sVersion, shoot.Spec.Kubernetes.Version, c.Images...)
		if err != nil {
			return nil, errors.Wrapf(err, "could not inject chart '%s' images for cluster '%s'", c.Name, clusterName)
		}
	}

	// Add subchart values
	for _, sc := range c.SubCharts {
		scValues, err := sc.getValues(clusterName, k8sVersion, shoot, imageVector, checksums)
		if err != nil {
			return nil, err
		}
		values[sc.Name] = scValues
	}

	return values, nil
}

// Delete deletes this chart's objects from the given namespace using the given client.
func (c *Chart) Delete(ctx context.Context, client client.Client, namespace string) error {
	// Delete objects
	for _, o := range c.Objects {
		if err := o.Delete(ctx, client, namespace); err != nil {
			return errors.Wrap(err, "could not delete chart '%s' object")
		}
	}

	// Delete subchart objects
	for _, sc := range c.SubCharts {
		if err := sc.Delete(ctx, client, namespace); err != nil {
			return err
		}
	}

	return nil
}

// Delete deletes this object from the given namespace using the given client.
func (o *Object) Delete(ctx context.Context, client client.Client, namespace string) error {
	obj := o.Type.DeepCopyObject()
	kind := obj.GetObjectKind().GroupVersionKind().Kind
	key := objectKey(namespace, o.Name)
	if err := client.Get(ctx, key, obj); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return errors.Wrapf(err, "could not get %s '%s'", kind, key.String())
	}
	if err := client.Delete(ctx, obj); err != nil {
		return errors.Wrapf(err, "could not delete %s '%s'", kind, key.String())
	}
	return nil
}

func objectKey(namespace, name string) client.ObjectKey {
	return client.ObjectKey{Namespace: namespace, Name: name}
}
