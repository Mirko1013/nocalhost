/*
* Copyright (C) 2020 THL A29 Limited, a Tencent company.  All rights reserved.
* This source code is licensed under the Apache License Version 2.0.
*/

package cmds

import (
	"github.com/spf13/cobra"
	"nocalhost/internal/nhctl/nocalhost"
	"nocalhost/pkg/nhctl/log"
)

func init() {
	dbSizeCmd.Flags().StringVar(&appName, "app", "", "List leveldb data of specified application")
	//pvcListCmd.Flags().StringVar(&pvcFlags.Svc, "controller", "", "List PVCs of specified service")
	dbCmd.AddCommand(dbSizeCmd)
}

var dbSizeCmd = &cobra.Command{
	Use:   "size [NAME]",
	Short: "Get all leveldb data",
	Long:  `Get all leveldb data`,
	Run: func(cmd *cobra.Command, args []string) {
		size, err := nocalhost.GetApplicationDbSize(nameSpace, appName)
		must(err)
		log.Info(size)
	},
}
