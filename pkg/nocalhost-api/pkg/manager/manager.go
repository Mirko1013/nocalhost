/*
* Copyright (C) 2020 THL A29 Limited, a Tencent company.  All rights reserved.
* This source code is licensed under the Apache License Version 2.0.
 */

package manager

import (
	"nocalhost/pkg/nocalhost-api/pkg/manager/mesh"
	"nocalhost/pkg/nocalhost-api/pkg/manager/vcluster"
)

var VClusterSharedManagerFactory = vcluster.NewSharedManagerFactory()
var MeshSharedManagerFactory = mesh.NewSharedManagerFactory()