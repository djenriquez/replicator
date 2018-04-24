package aws

import (
	"fmt"
	"time"

	metrics "github.com/armon/go-metrics"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/ec2"

	"github.com/elsevier-core-engineering/replicator/logging"
)

// translateIptoID translates the IP address of a node to the EC2 instance ID.
func translateIptoID(ip, region string) (id string, err error) {
	sess := session.Must(session.NewSession())
	svc := ec2.New(sess, &aws.Config{Region: aws.String(region)})

	params := &ec2.DescribeInstancesInput{
		DryRun: aws.Bool(false),
		Filters: []*ec2.Filter{
			{
				Name: aws.String("private-ip-address"),
				Values: []*string{
					aws.String(ip),
				},
			},
		},
	}
	resp, err := svc.DescribeInstances(params)

	if err != nil {
		logging.Error("cloud/aws: unable to convert node IP to AWS EC2 "+
			"instance ID: %v", err)
		return "", err
	}

	if resp == nil || resp.Reservations == nil || len(resp.Reservations) < 1 || resp.Reservations[0].Instances == nil || len(resp.Reservations[0].Instances) < 1 {
		return "", fmt.Errorf("cloud/aws: DescribeInstances returned an empty response")
	}

	return *resp.Reservations[0].Instances[0].InstanceId, nil
}

// terminateInstance terminates a specified EC2 instance and confirms success.
func terminateInstance(instanceID, region string) error {
	// Setup the session and the EC2 service link to use for this operation.
	sess := session.Must(session.NewSession())
	svc := ec2.New(sess, &aws.Config{Region: aws.String(region), MaxRetries: aws.Int(3)})

	// Setup parameters for the termination API request.
	tparams := &ec2.TerminateInstancesInput{
		InstanceIds: []*string{
			aws.String(instanceID),
		},
		DryRun: aws.Bool(false),
	}

	// Call the API to terminate the instance.
	logging.Info("cloud/aws: terminating instance %s", instanceID)
	if _, err := svc.TerminateInstances(tparams); err != nil {
		return err
	}

	// Setup our timeout and ticker value.
	ticker := time.NewTicker(time.Second * time.Duration(10))
	defer ticker.Stop()

	timeOut := time.NewTicker(time.Minute * 3)
	defer timeOut.Stop()

	logging.Info("cloud/aws: confirming successful termination of "+
		"instance %v", instanceID)

	for {
		select {
		case <-timeOut.C:
			return fmt.Errorf("timeout reached while attempting to confirm "+
				"the termination of instance %v", instanceID)

		case <-ticker.C:
			// Setup the parameters to call the instance status endpoint so that we
			// can discover the status of the terminating instance.
			params := &ec2.DescribeInstanceStatusInput{
				DryRun:              aws.Bool(false),
				IncludeAllInstances: aws.Bool(true),
				InstanceIds: []*string{
					aws.String(instanceID),
				},
			}

			resp, err := svc.DescribeInstanceStatus(params)
			if err != nil {
				logging.Error("cloud/aws: failed to desribe status of instance "+
					"%v: %v", instanceID, err)
				return err
			}

			if *resp.InstanceStatuses[0].InstanceState.Name == "terminated" {
				logging.Info("cloud/aws: successfully confirmed the termination "+
					"of instance %v", instanceID)

				metrics.IncrCounter([]string{"cluster", "aws",
					"instance_terminations"}, 1)

				return nil
			}
		}
	}
}
