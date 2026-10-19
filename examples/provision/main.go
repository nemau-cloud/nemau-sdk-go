// Command provision creates a network, a subnet, a security group and an
// instance, then deletes them. It is the SDK's end-to-end example.
//
//	export NEMAU_API_URL=https://api.nemau-cloud.com
//	export NEMAU_ACCESS_KEY_ID=...  NEMAU_ACCESS_KEY_SECRET=...
//	go run ./examples/provision -image ubuntu-24.04
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"time"

	nemau "github.com/nemau-cloud/nemau-sdk-go"
)

func main() {
	image := flag.String("image", "", "image id to boot (see: nemau compute images)")
	name := flag.String("name", "sdk-example", "name prefix for the created resources")
	keep := flag.Bool("keep", false, "leave the resources in place instead of deleting them")
	flag.Parse()

	if *image == "" {
		log.Fatal("-image is required")
	}

	// Ctrl-C cancels every wait: the SDK takes its deadlines from the context,
	// so an interrupt stops polling.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, 20*time.Minute)
	defer cancel()

	client, err := nemau.NewClient()
	if err != nil {
		log.Fatalf("client: %v", err)
	}
	fmt.Printf("api %s, credential %s\n", client.BaseURL(), client.DescribeCredential())

	network, err := client.Network.CreateNetworkAndWait(ctx, *name+"-net", "created by the SDK example")
	if err != nil {
		log.Fatalf("network: %v", err)
	}
	fmt.Printf("network %s (%s)\n", network.ID, network.Name)

	subnetRef, err := client.Network.CreateSubnet(ctx, network.ID, nemau.CreateSubnetRequest{
		Name: *name + "-subnet",
		CIDR: "10.42.0.0/24",
	})
	if err != nil {
		log.Fatalf("subnet: %v", err)
	}
	if _, err := client.WaitFor(ctx, subnetRef); err != nil {
		log.Fatalf("subnet: %v", err)
	}
	fmt.Println("subnet 10.42.0.0/24 ready")

	sgRef, err := client.Network.CreateSecurityGroup(ctx, *name+"-sg", "ssh only")
	if err != nil {
		log.Fatalf("security group: %v", err)
	}
	sgID, _, err := waitForID(ctx, client, sgRef, "security_group_id")
	if err != nil {
		log.Fatalf("security group: %v", err)
	}
	ruleRef, err := client.Network.CreateSecurityGroupRule(ctx, sgID, nemau.SecurityGroupRuleRequest{
		Direction:      "ingress",
		Protocol:       "tcp",
		PortRangeMin:   22,
		PortRangeMax:   22,
		RemoteIPPrefix: "0.0.0.0/0",
	})
	if err != nil {
		log.Fatalf("rule: %v", err)
	}
	if _, err := client.WaitFor(ctx, ruleRef); err != nil {
		log.Fatalf("rule: %v", err)
	}
	fmt.Printf("security group %s allows tcp/22\n", sgID)

	instance, err := client.Compute.CreateInstanceAndWait(ctx, nemau.CreateInstanceRequest{
		Name:           *name,
		ImageID:        *image,
		VCPUs:          2,
		RAMMB:          4096,
		DiskGB:         40,
		NetworkID:      network.ID,
		SecurityGroups: []string{sgID},
	})
	if err != nil {
		log.Fatalf("instance: %v", err)
	}
	fmt.Printf("instance %s is %s, addresses %v\n", instance.ID, instance.Status, instance.Addresses)

	if *keep {
		fmt.Println("-keep set; leaving everything in place")
		return
	}

	// Delete in reverse dependency order: the network cannot be deleted while
	// the instance still has an interface on it.
	//
	// The closure takes (ref, err), the pair every write in the SDK returns, so
	// a delete call can be passed to it directly.
	fmt.Println("tearing down")
	wait := func(ref *nemau.TaskRef, err error) {
		if err != nil {
			log.Fatalf("teardown: %v", err)
		}
		if _, err := client.WaitFor(ctx, ref); err != nil {
			log.Fatalf("teardown: %v", err)
		}
	}
	wait(client.Compute.DeleteInstance(ctx, instance.ID))
	wait(client.Network.DeleteSecurityGroup(ctx, sgID))
	wait(client.Network.DeleteNetwork(ctx, network.ID))
	fmt.Println("done")
}

func waitForID(ctx context.Context, c *nemau.Client, ref *nemau.TaskRef, keys ...string) (string, *nemau.Task, error) {
	task, err := c.WaitFor(ctx, ref)
	if err != nil {
		return "", task, err
	}
	if task.ResourceID != nil && *task.ResourceID != "" {
		return *task.ResourceID, task, nil
	}
	if id := task.ResultString(keys...); id != "" {
		return id, task, nil
	}
	return "", task, fmt.Errorf("task %s produced no resource id", task.ID)
}
