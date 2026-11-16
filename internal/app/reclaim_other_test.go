//go:build !darwin && !linux

package app

const reclaimOwnerEnv = "HOXI_TEST_RECLAIM_OWNER"

func runReclaimOwner() int { return 2 }
