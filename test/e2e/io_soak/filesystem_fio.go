package io_soak

import (
	"e2e-basic/common"
	"e2e-basic/common/e2e_config"

	"fmt"
	"time"

	coreV1 "k8s.io/api/core/v1"
)

// IO soak filesystem fio job
type FioFsSoakJob struct {
	volName  string
	scName   string
	podName  string
	id       int
	duration int
}

func (job FioFsSoakJob) makeVolume() {
	common.MkPVC(common.DefaultVolumeSizeMb, job.volName, job.scName, common.VolFileSystem, common.NSDefault)
}

func (job FioFsSoakJob) removeVolume() {
	common.RmPVC(job.volName, job.scName, common.NSDefault)
}

func (job FioFsSoakJob) makeTestPod(selector map[string]string) (*coreV1.Pod, error) {
	pod := common.CreateFioPodDef(job.podName, job.volName, common.VolFileSystem, common.NSDefault)
	pod.Spec.NodeSelector = selector

	image := "" + e2e_config.GetConfig().Registry + "/mayastor/e2e-fio"
	pod.Spec.Containers[0].Image = image

	args := []string{
		"--",
		"--time_based",
		fmt.Sprintf("--runtime=%d", job.duration),
		fmt.Sprintf("--filename=%s", common.FioFsFilename),
		fmt.Sprintf("--thinktime=%d", GetThinkTime(job.id)),
		fmt.Sprintf("--thinktime_blocks=%d", GetThinkTimeBlocks(job.id)),
		fmt.Sprintf("--size=%dm", common.DefaultFioSizeMb),
	}
	args = append(args, FioArgs...)
	pod.Spec.Containers[0].Args = args

	pod, err := common.CreatePod(pod, common.NSDefault)
	return pod, err
}

func (job FioFsSoakJob) removeTestPod() error {
	return common.DeletePod(job.podName, common.NSDefault)
}

func (job FioFsSoakJob) run(duration time.Duration, doneC chan<- string, errC chan<- error) {
	thinkTime := 1 // 1 microsecond
	thinkTimeBlocks := 1000

	FioDutyCycles := e2e_config.GetConfig().IOSoakTest.FioDutyCycles
	if len(FioDutyCycles) != 0 {
		ixp := job.id % len(FioDutyCycles)
		thinkTime = FioDutyCycles[ixp].ThinkTime
		thinkTimeBlocks = FioDutyCycles[ixp].ThinkTimeBlocks
	}

	RunIoSoakFio(
		job.podName,
		duration,
		thinkTime,
		thinkTimeBlocks,
		common.VolFileSystem,
		doneC,
		errC,
	)
}

func (job FioFsSoakJob) getPodName() string {
	return job.podName
}

func MakeFioFsJob(scName string, id int, duration time.Duration) FioFsSoakJob {
	nm := fmt.Sprintf("fio-filesystem-%s-%d", scName, id)
	return FioFsSoakJob{
		volName:  nm,
		scName:   scName,
		podName:  nm,
		id:       id,
		duration: int(duration.Seconds()),
	}
}
