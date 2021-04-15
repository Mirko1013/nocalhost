/*
 * Tencent is pleased to support the open source community by making Nocalhost available.,
 * Copyright (C) 2019 THL A29 Limited, a Tencent company. All rights reserved.
 * Licensed under the MIT License (the "License"); you may not use this file except
 * in compliance with the License. You may obtain a copy of the License at
 * http://opensource.org/licenses/MIT
 * Unless required by applicable law or agreed to in writing, software distributed under,
 * the License is distributed on an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND,
 * either express or implied. See the License for the specific language governing permissions and
 * limitations under the License.
 */

package app

import (
	"context"
	"fmt"
	"math/rand"
	"nocalhost/internal/nhctl/profile"
	"nocalhost/internal/nhctl/utils"
	"nocalhost/pkg/nhctl/clientgoutils"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/pkg/errors"
	"gopkg.in/yaml.v2"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"nocalhost/pkg/nhctl/log"
	"nocalhost/pkg/nhctl/tools"
)

//  Install a nocalhost application
func (a *Application) Install(ctx context.Context, flags *HelmFlags) error {

	err := a.InstallDepConfigMap()
	if err != nil {
		return errors.Wrap(err, "failed to install dep config map")
	}
	//appProfile, err := a.GetProfile()
	//if err != nil {
	//	return err
	//}
	switch a.profileV2.AppType {
	case string(Helm), string(HelmLocal):
		err = a.installHelm(flags, false)
	case string(HelmRepo):
		err = a.installHelm(flags, true)
	case string(Manifest), string(ManifestLocal):
		err = a.InstallManifest()
	case string(KustomizeGit):
		// err = a.InstallKustomizeWithKubectl()
		err = a.InstallKustomize()
	default:
		return errors.New(
			fmt.Sprintf(
				"unsupported application type, must be %s, %s or %s",
				Helm,
				HelmRepo,
				Manifest,
			),
		)
	}
	if err != nil {
		a.cleanUpDepConfigMap() // clean up dep config map

		// Clean up helm release after failed
		if a.IsHelm() {
			a.uninstallHelm()
		}
		return err
	}

	a.SetInstalledStatus(true)
	return nil
}

func (a *Application) InstallKustomize() error {
	resourcesPath := a.GetResourceDir()
	if len(resourcesPath) > 1 {
		log.Warn(`There are multiple resourcesPath settings, will use first one`)
	}
	useResourcePath := resourcesPath[0]
	err := a.client.ApplyForCreate(
		[]string{},
		true,
		StandardNocalhostMetas(a.Name, a.NameSpace),
		useResourcePath,
	)
	if err != nil {
		return err
	}
	return nil
}

func (a *Application) InstallKustomizeWithKubectl() error {
	err := utils.CheckKubectlVersion(14)
	if err != nil {
		log.Warn(err.Error())
	}
	resourcesPath := a.GetResourceDir()
	if len(resourcesPath) > 1 {
		log.Warn(`There are multiple resourcesPath settings, will use first one`)
	}
	useResourcePath := resourcesPath[0]
	commonParams := []string{"apply", "-k", useResourcePath}
	if a.NameSpace != "" {
		commonParams = append(commonParams, "--namespace", a.NameSpace)
	}
	if a.KubeConfig != "" {
		commonParams = append(commonParams, "--kubeconfig", a.KubeConfig)
	}
	_, err = tools.ExecCommand(nil, true, "kubectl", commonParams...)
	if err != nil {
		return err
	}
	return nil
}

func (a *Application) InstallManifest() error {
	a.loadPreInstallAndInstallManifest()
	a.preInstall()

	// install manifest recursively, don't install pre-install workload again
	return errors.Wrap(a.installManifestRecursively(), "")
}

func (a *Application) installHelm(flags *HelmFlags, fromRepo bool) error {

	releaseName := a.Name
	commonParams := make([]string, 0)
	if a.NameSpace != "" {
		commonParams = append(commonParams, "--namespace", a.NameSpace)
	}
	if a.KubeConfig != "" {
		commonParams = append(commonParams, "--kubeconfig", a.KubeConfig)
	}
	if flags.Debug {
		commonParams = append(commonParams, "--debug")
	}

	var resourcesPath []string
	if !fromRepo {
		resourcesPath = a.GetResourceDir()
	}
	profileV2, err := profile.NewAppProfileV2ForUpdate(a.NameSpace, a.Name)
	if err != nil {
		return err
	}
	defer profileV2.CloseDb()

	installParams := []string{"install", releaseName}
	if !fromRepo {
		installParams = append(installParams, resourcesPath[0])
		log.Info("building dependency...")
		depParams := []string{"dependency", "build", resourcesPath[0]}
		depParams = append(depParams, commonParams...)
		if _, err = tools.ExecCommand(nil, true, "helm", depParams...); err != nil {
			return errors.Wrap(err, "fail to build dependency for helm app")
		}
	} else {
		chartName := flags.Chart
		if a.configV2 != nil && a.configV2.ApplicationConfig.Name != "" {
			chartName = a.configV2.ApplicationConfig.Name
		}
		if flags.RepoUrl != "" {
			installParams = append(installParams, chartName, "--repo", flags.RepoUrl)
			profileV2.HelmRepoUrl = flags.RepoUrl
		} else if flags.RepoName != "" {
			installParams = append(installParams, fmt.Sprintf("%s/%s", flags.RepoName, chartName))
			profileV2.HelmRepoName = flags.RepoName
		}
		if flags.Version != "" {
			installParams = append(installParams, "--version", flags.Version)
		}
		profileV2.ChartName = chartName
	}

	if flags.Wait {
		installParams = append(installParams, "--wait")
	}

	for _, set := range flags.Set {
		installParams = append(installParams, "--set", set)
	}

	if flags.Values != "" {
		installParams = append(installParams, "-f", flags.Values)
	}
	installParams = append(installParams, "--timeout", "60m")
	installParams = append(installParams, commonParams...)

	fmt.Println("install helm application, this may take several minutes, please waiting...")

	if _, err = tools.ExecCommand(nil, true, "helm", installParams...); err != nil {
		return errors.Wrap(err, "fail to install helm application")
	}

	profileV2.ReleaseName = releaseName
	profileV2.Save()
	log.Infof(
		`helm nocalhost app installed, use "helm list -n %s" to get the information of the helm release`,
		a.NameSpace,
	)
	return nil
}

func (a *Application) InstallDepConfigMap() error {
	appDep := a.GetDependencies()
	appEnv := a.GetInstallEnvForDep()
	if len(appDep) > 0 || len(appEnv.Global) > 0 || len(appEnv.Service) > 0 {
		var depForYaml = &struct {
			Dependency  []*SvcDependency  `json:"dependency" yaml:"dependency"`
			ReleaseName string            `json:"releaseName" yaml:"releaseName"`
			InstallEnv  *InstallEnvForDep `json:"env" yaml:"env"`
		}{
			Dependency: appDep,
			InstallEnv: appEnv,
		}

		profileV2, err := profile.NewAppProfileV2ForUpdate(a.NameSpace, a.Name)
		if err != nil {
			return err
		}
		defer profileV2.CloseDb()
		// release name a.Name
		if profileV2.AppType != string(Manifest) {
			depForYaml.ReleaseName = a.Name
		}
		yamlBytes, err := yaml.Marshal(depForYaml)
		if err != nil {
			return errors.Wrap(err, "")
		}

		dataMap := make(map[string]string, 0)
		dataMap["nocalhost"] = string(yamlBytes)

		configMap := &corev1.ConfigMap{
			Data: dataMap,
		}

		var letterRunes = []rune("abcdefghijklmnopqrstuvwxyz")
		rand.Seed(time.Now().UnixNano())
		b := make([]rune, 4)
		for i := range b {
			b[i] = letterRunes[rand.Intn(len(letterRunes))]
		}
		generateName := fmt.Sprintf("%s-%s", DependenceConfigMapPrefix, string(b))
		configMap.Name = generateName
		if configMap.Labels == nil {
			configMap.Labels = make(map[string]string, 0)
		}
		configMap.Labels["use-for"] = "nocalhost-dep"
		if _, err = a.client.ClientSet.CoreV1().ConfigMaps(a.NameSpace).
			Create(context.TODO(), configMap, metav1.CreateOptions{}); err != nil {
			return errors.Wrap(
				err,
				fmt.Sprintf("fail to create dependency config %s", configMap.Name),
			)
		} else {
			profileV2.DependencyConfigMapName = configMap.Name
			profileV2.Save()
		}
	}
	log.Logf("Dependency config map installed")
	return nil
}

func (a *Application) installManifestRecursively() error {
	log.Logf("%d manifest files to be installed", len(a.installManifest))
	if len(a.installManifest) > 0 {
		err := a.client.ApplyForCreate(
			a.installManifest,
			true,
			StandardNocalhostMetas(a.Name, a.NameSpace),
			"",
		)
		if err != nil {
			return err
		}
	} else {
		log.Logf("nothing need to be installed ??")
	}
	return nil
}

func (a *Application) SetInstalledStatus(is bool) {
	profileV2, err := profile.NewAppProfileV2ForUpdate(a.NameSpace, a.Name)
	if err != nil {
		return
	}
	defer profileV2.CloseDb()
	profileV2.Installed = is
	profileV2.Save()
}

func (a *Application) loadInstallManifest() {
	a.installManifest = clientgoutils.
		LoadValidManifest(a.GetResourceDir(),
			append(a.getIgnoredPath(), a.getPreInstallFiles()...))
}

func (a *Application) loadPreInstallAndInstallManifest() {
	a.loadSortedPreInstallManifest()
	a.loadInstallManifest()
}

func (a *Application) loadUpgradeManifest(resourcePath []string) {
	a.loadUpgradeSortedPreInstallManifest()
	a.loadUpgradeInstallManifest(resourcePath)
}

func (a *Application) loadUpgradeInstallManifest(upgradeResourcePath []string) {
	a.upgradeInstallManifest = clientgoutils.
		LoadValidManifest(a.getUpgradeResourceDir(upgradeResourcePath),
			append(a.getUpgradeIgnoredPath(), a.getUpgradePreInstallFiles()...))
}

func (a *Application) ignoredInUpgrade(manifest string) bool {
	for _, pre := range a.upgradeSortedPreInstallManifest {
		if pre == manifest {
			return true
		}
	}
	return false
}

func (a *Application) loadUpgradeSortedPreInstallManifest() {
	appProfile, _ := a.GetProfile()
	result := make([]string, 0)
	if appProfile.PreInstall != nil {
		sort.Sort(profile.ComparableItems(appProfile.PreInstall))
		for _, item := range appProfile.PreInstall {
			itemPath := filepath.Join(a.getUpgradeGitDir(), item.Path)
			if _, err2 := os.Stat(itemPath); err2 != nil {
				log.Warnf("%s is not a valid pre install manifest : %s\n", itemPath, err2.Error())
				continue
			}
			result = append(result, itemPath)
		}
	}
	a.upgradeSortedPreInstallManifest = result
}

func (a *Application) loadSortedPreInstallManifest() {
	appProfile, _ := a.GetProfile()
	result := make([]string, 0)
	if appProfile.PreInstall != nil {
		sort.Sort(profile.ComparableItems(appProfile.PreInstall))
		for _, item := range appProfile.PreInstall {
			itemPath := filepath.Join(a.getGitDir(), item.Path)
			if _, err2 := os.Stat(itemPath); err2 != nil {
				log.Warnf("%s is not a valid pre install manifest : %s\n", itemPath, err2.Error())
				continue
			}
			result = append(result, itemPath)
		}
	}
	a.sortedPreInstallManifest = result
}

func (a *Application) preInstall() {

	if len(a.sortedPreInstallManifest) > 0 {
		log.Info("Run pre-install...")
		for _, item := range a.sortedPreInstallManifest {
			err := a.client.Create(item, true, false, StandardNocalhostMetas(a.Name, a.NameSpace))
			if err != nil {
				log.Warnf("error occurs when install %s : %s\n", item, err.Error())
			}
		}
	}
}
