/*
Copyright 2020 The Nocalhost Authors.
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

package app

import (
	"fmt"
	"nocalhost/internal/nhctl/syncthing"
	"runtime"
	"strconv"
	"strings"

	"github.com/pkg/errors"

	"nocalhost/pkg/nhctl/log"
)

func (a *Application) StopAllPortForward(svcName string) error {
	appProfile, err := a.GetProfile()
	if err != nil {
		return err
	}
	svcProfile := appProfile.FetchSvcProfileV2FromProfile(svcName)

	for _, portForward := range svcProfile.DevPortForwardList {
		err = a.EndDevPortForward(svcName, portForward.LocalPort, portForward.RemotePort)
		if err != nil {
			log.WarnE(err, "")
		}
	}
	return nil
}

// port format 8080:80
func (a *Application) StopPortForwardByPort(svcName, port string) error {

	ports := strings.Split(port, ":")
	localPort, err := strconv.Atoi(ports[0])
	if err != nil {
		return errors.Wrap(err, "")
	}
	remotePort, err := strconv.Atoi(ports[1])
	if err != nil {
		return errors.Wrap(err, "")
	}
	return a.EndDevPortForward(svcName, localPort, remotePort)
}

func (a *Application) StopFileSyncOnly(svcName string) error {
	var err error

	pf, err := a.GetPortForwardForSync(svcName)
	if err != nil {
		log.WarnE(err, "")
	}
	if pf != nil {
		if err = a.EndDevPortForward(svcName, pf.LocalPort, pf.RemotePort); err != nil {
			log.WarnE(err, "")
		}
	}

	// Deprecated: port-forward has moved to daemon server
	portForwardPid, portForwardFilePath, err := a.GetBackgroundSyncPortForwardPid(svcName, false)
	if err != nil {
		log.Warn("Failed to get background port-forward pid file, ignored")
	}
	if portForwardPid != 0 {
		err = syncthing.Stop(portForwardPid, portForwardFilePath, "port-forward", true)
		if err != nil {
			log.Warnf("Failed stop port-forward progress pid %d, please run `kill -9 %d` by manual, err: %s\n", portForwardPid, portForwardPid, err)
		}
	}

	// read and clean up pid file
	syncthingPid, syncThingPath, err := a.GetBackgroundSyncThingPid(svcName, false)
	if err != nil {
		log.Warn("Failed to get background syncthing pid file, ignored")
	}
	if syncthingPid != 0 {
		err = syncthing.Stop(syncthingPid, syncThingPath, "syncthing", true)
		if err != nil {
			if runtime.GOOS == "windows" {
				// in windows, it will raise a "Access is denied" err when killing progress, so we can ignore this err
				fmt.Printf("attempt to terminate syncthing process(pid: %d), you can run `tasklist | findstr %d` to make sure process was exited\n", portForwardPid, portForwardPid)
			} else {
				log.Warnf("Failed to terminate syncthing process(pid: %d), please run `kill -9 %d` manually, err: %s\n", portForwardPid, portForwardPid, err)
			}
		}
	}

	if err == nil { // none of them has error
		fmt.Printf("Background port-forward process: %d and  syncthing process: %d terminated.\n", portForwardPid, syncthingPid)
	}
	return err
}

func (a *Application) StopSyncAndPortForwardProcess(svcName string, cleanRemoteSecret bool) error {
	err := a.StopFileSyncOnly(svcName)

	log.Info("Stopping port forward")
	if err = a.StopAllPortForward(svcName); err != nil {
		log.WarnE(err, "")
	}

	// Clean up secret
	if cleanRemoteSecret {
		appProfile, _ := a.GetProfile()
		svcProfile := appProfile.FetchSvcProfileV2FromProfile(svcName)
		if svcProfile.SyncthingSecret != "" {
			log.Debugf("Cleaning up secret %s", svcProfile.SyncthingSecret)
			err = a.client.DeleteSecret(svcProfile.SyncthingSecret)
			if err != nil {
				log.WarnE(err, "Failed to clean up syncthing secret")
			} else {
				svcProfile.SyncthingSecret = ""
			}
		}
	}

	// set profile status
	// set port-forward port and ignore result
	// err = a.SetSyncthingPort(svcName, 0, 0, 0, 0)
	err = a.SetSyncthingProfileEndStatus(svcName)
	return err
}

func (a *Application) DevEnd(svcName string, reset bool) error {
	if err := a.RollBack(svcName, reset); err != nil {
		if !reset {
			return err
		}
		log.WarnE(err, "something incorrect occurs when rolling back")
	}

	if err := a.appMeta.DeploymentDevEnd(svcName); err != nil {
		log.WarnE(err, "something incorrect occurs when updating secret")
	}

	if err := a.StopSyncAndPortForwardProcess(svcName, true); err != nil {
		log.WarnE(err, "something incorrect occurs when stopping sync process")
	}
	return nil
}
