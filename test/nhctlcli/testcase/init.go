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

package testcase

import (
	"bufio"
	"context"
	"fmt"
	"github.com/imroc/req"
	"github.com/pkg/errors"
	"gopkg.in/yaml.v3"
	"io"
	"io/ioutil"
	"nocalhost/internal/nhctl/app"
	"nocalhost/internal/nhctl/profile"
	"nocalhost/internal/nhctl/request"
	"nocalhost/pkg/nhctl/clientgoutils"
	"nocalhost/pkg/nhctl/log"
	"nocalhost/pkg/nhctl/tools"
	"nocalhost/pkg/nocalhost-api/app/api/v1/service_account"
	"nocalhost/test/nhctlcli"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strings"
)

var StopChan = make(chan int32, 1)
var StatusChan = make(chan int32, 1)
var WebServerEndpointChan = make(chan string)

func GetVersion() (v1 string, v2 string) {
	commitId := os.Getenv("COMMIT_ID")
	var tags []string
	if len(os.Getenv("TAG")) != 0 {
		tags = strings.Split(strings.TrimSuffix(os.Getenv("TAG"), "\n"), " ")
	}
	if commitId == "" && len(tags) == 0 {
		panic(fmt.Sprintf("test case failed, can not found any version, commit_id: %v, tag: %v", commitId, tags))
	}
	if len(tags) >= 2 {
		v1 = tags[0]
		v2 = tags[1]
	} else if len(tags) == 1 {
		v1 = tags[0]
	} else {
		v1 = commitId
	}
	log.Infof("version info, v1: %s, v2: %s", v1, v2)
	return
}

func InstallNhctl(version string) {
	var name string
	var outputName string
	var needChmod bool
	if strings.Contains(runtime.GOOS, "darwin") {
		name = "nhctl-darwin-amd64"
		outputName = "nhctl"
		needChmod = true
	} else if strings.Contains(runtime.GOOS, "windows") {
		name = "nhctl-windows-amd64.exe"
		outputName = "nhctl.exe"
		needChmod = false
	} else {
		name = "nhctl-linux-amd64"
		outputName = "nhctl"
		needChmod = true
	}

	str := "curl --fail -s -L \"https://codingcorp-generic.pkg.coding.net/nocalhost/nhctl/%s?version=%s\" -o " + outputName
	cmd := exec.Command("sh", "-c", fmt.Sprintf(str, name, version))
	nhctlcli.Runner.RunPanicIfError(cmd)

	// unix and linux needs to add x permission
	if needChmod {
		cmd = exec.Command("sh", "-c", "chmod +x nhctl")
		nhctlcli.Runner.RunPanicIfError(cmd)
		cmd = exec.Command("sh", "-c", "mv ./nhctl /usr/local/bin/nhctl")
		nhctlcli.Runner.RunPanicIfError(cmd)
	}
}

func Init(nhctl *nhctlcli.CLI) {
	cmd := nhctl.CommandWithNamespace(context.Background(), "init", "nocalhost", "demo", "-p", "7000", "--force")
	fmt.Printf("Running command: %s\n", cmd.Args)
	stdoutRead, err := cmd.StdoutPipe()
	if err != nil {
		panic(errors.Wrap(err, "stdout error"))
	}
	if err := cmd.Start(); err != nil {
		_ = cmd.Process.Kill()
		panic(fmt.Sprintf("nhctl init error: %v", err))
	}
	defer cmd.Wait()
	defer stdoutRead.Close()
	lineBody := bufio.NewReaderSize(stdoutRead, 1024)
	go func() {
		for {
			line, isPrefix, err := lineBody.ReadLine()
			if err != nil && err != io.EOF && !strings.Contains(err.Error(), "closed") {
				fmt.Printf("command error: %v, log : %v", err, string(line))
				StatusChan <- 1
			}
			if len(line) != 0 && !isPrefix {
				fmt.Println(string(line))
			}
			if strings.Contains(string(line), "Nocalhost init completed") {
				reg := regexp.MustCompile("http://(.*?):(.*?) ")
				submatch := reg.FindStringSubmatch(string(line))
				if len(submatch) == 0 {
					StatusChan <- 1
					break
				}
				WebServerEndpointChan <- submatch[0]
				StatusChan <- 0
				break
			}
		}
	}()
	for {
		select {
		case stat := <-StopChan:
			switch stat {
			case 0: // ok
				_ = cmd.Process.Kill()
				return
			default:
				_ = cmd.Process.Kill()
				panic("test case failed, exiting")
			}
		}
	}
}

func StatusCheck(nhctl *nhctlcli.CLI, moduleName string) {
	cmd := nhctl.Command(context.Background(), "describe", "bookinfo", "-d", moduleName)
	stdout, stderr, err := nhctlcli.Runner.Run(cmd)
	if err != nil {
		panic(fmt.Sprintf("Run command: %s, error: %v, stdout: %s, stderr: %s", cmd.Args, err, stdout, stderr))
	}
	service := profile.SvcProfileV2{}
	_ = yaml.Unmarshal([]byte(stdout), &service)
	if !service.Developing {
		panic("test case failed, should be developing")
	}
	if !service.PortForwarded {
		panic("test case failed, should be port forwarding")
	}
	if !service.Syncing {
		panic("test case failed, should be synchronizing")
	}
}

var WebServerServiceAccountApi = "/v1/plugin/service_accounts"

func GetKubeconfig(ns, webEndpoint, kubeconfig string) string {
	client, err := clientgoutils.NewClientGoUtils(kubeconfig, ns)
	log.Debugf("kubeconfig %s \n", kubeconfig)
	if err != nil || client == nil {
		log.Fatalf("new go client fail, err %s, or check you kubeconfig\n", err)
		panic("new go client fail, or check you kubeconfig")
	}
	kubectl, err := tools.CheckThirdPartyCLI()
	res := request.
		NewReq(webEndpoint, kubeconfig, kubectl, ns, 7000).
		Login(app.DefaultInitUserEmail, app.DefaultInitPassword)
	header := req.Header{
		"Accept":        "application/json",
		"Authorization": "Bearer " + res.AuthToken,
	}
	r, err := req.New().Get(webEndpoint+WebServerServiceAccountApi, header)
	if err != nil {
		log.Fatalf("init fail, add dev space fail, err: %s", err)
		panic("init fail, add dev space fail")
	}
	re := Response{}
	err = r.ToJSON(&re)
	if re.Code != 0 || len(re.Data) == 0 || re.Data[0] == nil {
		log.Fatalf("init fail, add dev space, err: %s", re.Message)
		panic("init fail, add dev space")
	}
	config := re.Data[0].KubeConfig
	if config != "" {
		f, _ := ioutil.TempFile("/tmp", "*kubeconfig")
		_, _ = f.WriteString(config)
		_ = f.Sync()
		return f.Name()
	} else {
		fmt.Println("Not found")
		panic("Not found")
	}

}

type Response struct {
	Code    int                                    `json:"code"`
	Message string                                 `json:"message"`
	Data    []*service_account.ServiceAccountModel `json:"data"`
}
