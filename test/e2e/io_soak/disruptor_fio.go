package io_soak

import (
	"e2e-basic/common"
	"e2e-basic/common/e2e_config"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"fmt"
	"time"

	. "github.com/onsi/gomega"

	coreV1 "k8s.io/api/core/v1"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

var disruptorJobs []FioDisruptorJob
var disruptorScNames []string

// IO soak disruptor fio  job
type FioDisruptorJob struct {
	volName    string
	scName     string
	podName    string
	id         int
	faultDelay int
	ready 	   bool
}

func (job FioDisruptorJob) makeVolume() {
	common.MkPVC(common.DefaultVolumeSizeMb, job.volName, job.scName, common.VolRawBlock, NSDisrupt)
}

func (job FioDisruptorJob) removeVolume() {
	common.RmPVC(job.volName, job.scName, NSDisrupt)
}

func (job FioDisruptorJob) makeTestPod(selector map[string]string) (*coreV1.Pod, error) {
	pod := common.CreateFioPodDef(job.podName, job.volName, common.VolRawBlock, NSDisrupt)
	pod.Spec.NodeSelector = selector
	pod.Spec.RestartPolicy = coreV1.RestartPolicyAlways

	image := "" + e2e_config.GetConfig().Registry + "/mayastor/e2e-fio"
	pod.Spec.Containers[0].Image = image

	args := []string{
		"segfault-after",
		fmt.Sprintf("%d", job.faultDelay),
		"--",
		"--time_based",
		fmt.Sprintf("--runtime=%d", job.faultDelay+100),
		fmt.Sprintf("--filename=%s", common.FioBlockFilename),
		fmt.Sprintf("--thinktime=%d", GetThinkTime(job.id)),
		fmt.Sprintf("--thinktime_blocks=%d", GetThinkTimeBlocks(job.id)),
	}
	args = append(args, FioArgs...)
	pod.Spec.Containers[0].Args = args

	pod, err := common.CreatePod(pod, NSDisrupt)
	return pod, err
}

func (job FioDisruptorJob) removeTestPod() error {
	return common.DeletePod(job.podName, NSDisrupt)
}

func (job FioDisruptorJob) run(duration time.Duration, doneC chan<- string, errC chan<- error) {
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
		common.VolRawBlock,
		doneC,
		errC,
	)
}

func (job FioDisruptorJob) getPodName() string {
	return job.podName
}

func MakeFioDisruptorJob(scName string, id int, segfaultDelay int) FioDisruptorJob {
	nm := fmt.Sprintf("fio-disruptor-%s-%d", scName, id)
	return FioDisruptorJob{
		volName:    nm,
		scName:     scName,
		podName:    nm,
		id:         id,
		faultDelay: segfaultDelay,
		ready: false,
	}
}

func DisruptorsInit(protocols []common.ShareProto, replicas int) {
	for _, proto := range protocols {
		scName := fmt.Sprintf("iosoak-disruptor-%s", proto)
		logf.Log.Info("Creating", "storage class", scName)
		err := common.MkStorageClass(scName, replicas, proto, common.NSDefault)
		Expect(err).ToNot(HaveOccurred())
		disruptorScNames = append(disruptorScNames, scName)
	}
}

func DisruptorsDeinit() {
	for _, scName := range disruptorScNames {
		err := common.RmStorageClass(scName)
		Expect(err).ToNot(HaveOccurred())
	}
}

func MakeDisruptors() {
	config := e2e_config.GetConfig().IOSoakTest.Disrupt
	count := config.PodCount
	err := common.MkNamespace(NSDisrupt)
	Expect(err).ToNot(HaveOccurred(), "Create namespace %s", NSDisrupt)

	idx := 1
	for idx <= count {
		for _, scName := range disruptorScNames {
			if idx > count {
				break
			}
			log.Log.Info("Creating", "job", "fio disruptor job", "id", idx)
			disruptorJobs = append(disruptorJobs, MakeFioDisruptorJob(scName, idx, config.FaultAfter))
			idx++
		}
	}

	for _, job := range disruptorJobs {
		job.makeVolume()
	}

	log.Log.Info("Creating disruptor test pods")
	// Create the job test pods
	for _, job := range disruptorJobs {
		pod, err := job.makeTestPod(AppNodeSelector)
		Expect(err).ToNot(HaveOccurred())
		Expect(pod).ToNot(BeNil())
	}

	// Empirically allocate  PodReadyTime seconds for each pod to transition to ready
	timeoutSecs := PodReadyTime * len(disruptorJobs)
	if timeoutSecs < 60 {
		timeoutSecs = 60
	}
	logf.Log.Info("Waiting for disruptor pods to be ready", "timeout seconds", timeoutSecs, "jobs", len(disruptorJobs))

	// Wait for the test pods to be ready,
	// This is a bit tricky we want assert that all disruptor pods have started,
	// however as disruptor pods restart, we have to be careful.
	// We try to detect the edge when disruptor pods transition
	// to ready and latch that as the disruptor pod is "ready"
	allReady := false
	for to:=0; to< timeoutSecs && !allReady; to+=1 {
		time.Sleep(1* time.Second)
		allReady = true
		for _, job := range disruptorJobs {
			if !job.ready {
				job.ready = common.IsPodRunning(job.getPodName(), NSDisrupt)
			}
			allReady = allReady && job.ready
		}
	}
	Expect(allReady).To(BeTrue(), "Timeout waiting to disruptor jobs to be ready")
}

func DestroyDisruptors() {
	for _, job := range disruptorJobs {
		err := job.removeTestPod()
		Expect(err).ToNot(HaveOccurred())
	}

	log.Log.Info("All runs complete, deleting volumes")
	for _, job := range disruptorJobs {
		job.removeVolume()
	}

	err := common.RmNamespace(NSDisrupt)
	Expect(err).ToNot(HaveOccurred(), "Delete namespace %s", NSDisrupt)
}
