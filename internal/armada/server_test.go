package armada

import (
	"context"
	"github.com/G-Research/k8s-batch/internal/armada/api"
	"github.com/G-Research/k8s-batch/internal/armada/configuration"
	"github.com/G-Research/k8s-batch/internal/common"
	"github.com/alicebob/miniredis"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"log"
	"testing"
)

func TestSubmitJob(t *testing.T) {
	withRunningServer(func(client api.SubmitClient, leaseClient api.AggregatedQueueClient, ctx context.Context) {

		_, err := client.CreateQueue(ctx, &api.Queue{
			Name:           "test",
			PriorityFactor: 1,
		})
		assert.Empty(t, err)

		cpu, _ := resource.ParseQuantity("1")
		memory, _ := resource.ParseQuantity("512Mi")

		jobId := SubmitJob(client, ctx, cpu, memory, t)

		leasedResponse, err := leaseClient.LeaseJobs(ctx, &api.LeaseRequest{
			ClusterId: "test-cluster",
			Resources: common.ComputeResources{"cpu": cpu, "memory": memory},
		})
		assert.Empty(t, err)

		assert.Equal(t, 1, len(leasedResponse.Job))
		assert.Equal(t, jobId, leasedResponse.Job[0].Id)
	})
}

func TestCancelJob(t *testing.T) {
	withRunningServer(func(client api.SubmitClient, leaseClient api.AggregatedQueueClient, ctx context.Context) {

		_, err := client.CreateQueue(ctx, &api.Queue{
			Name:           "test",
			PriorityFactor: 1,
		})
		assert.Empty(t, err)

		cpu, _ := resource.ParseQuantity("1")
		memory, _ := resource.ParseQuantity("512Mi")

		SubmitJob(client, ctx, cpu, memory, t)
		SubmitJob(client, ctx, cpu, memory, t)

		leasedResponse, err := leaseClient.LeaseJobs(ctx, &api.LeaseRequest{
			ClusterId: "test-cluster",
			Resources: common.ComputeResources{"cpu": cpu, "memory": memory},
		})
		assert.Empty(t, err)
		assert.Equal(t, 1, len(leasedResponse.Job))

		cancelResult, err := client.CancelJobs(ctx, &api.JobCancelRequest{JobSetId: "set", Queue: "test"})
		assert.Empty(t, err)
		assert.Equal(t, 2, len(cancelResult.CancelledIds))

		renewed, err := leaseClient.RenewLease(ctx, &api.RenewLeaseRequest{
			ClusterId: "test-cluster",
			Ids:       []string{leasedResponse.Job[0].Id},
		})
		assert.Empty(t, err)
		assert.Equal(t, 0, len(renewed.Ids))

	})
}

func SubmitJob(client api.SubmitClient, ctx context.Context, cpu resource.Quantity, memory resource.Quantity, t *testing.T) string {
	response, err := client.SubmitJob(ctx, &api.JobRequest{
		PodSpec: &v1.PodSpec{
			Containers: []v1.Container{{
				Name:  "Container1",
				Image: "index.docker.io/library/ubuntu:latest",
				Args:  []string{"sleep", "10s"},
				Resources: v1.ResourceRequirements{
					Requests: v1.ResourceList{"cpu": cpu, "memory": memory},
					Limits:   v1.ResourceList{"cpu": cpu, "memory": memory},
				},
			},
			},
		},
		Priority: 0,
		Queue:    "test",
		JobSetId: "set",
	})
	assert.Empty(t, err)
	return response.JobId
}

func withRunningServer(action func(client api.SubmitClient, leaseClient api.AggregatedQueueClient, ctx context.Context)) {
	redis, err := miniredis.Run()
	if err != nil {
		panic(err)
	}
	defer redis.Close()

	// cleanup prometheus in case there are registered metrics already present
	prometheus.DefaultRegisterer = prometheus.NewRegistry()
	server, _ := Serve(&configuration.ArmadaConfig{
		GrpcPort: ":50051",
		Redis: configuration.RedisConfig{
			Addr: redis.Addr(),
			Db:   0,
		},
	})
	defer server.Stop()

	conn, err := grpc.Dial("localhost:50051", grpc.WithInsecure(), grpc.WithDefaultCallOptions(grpc.WaitForReady(true)))
	if err != nil {
		log.Fatalf("did not connect: %v", err)
	}
	defer conn.Close()

	client := api.NewSubmitClient(conn)
	leaseClient := api.NewAggregatedQueueClient(conn)
	ctx := context.Background()

	action(client, leaseClient, ctx)
}
