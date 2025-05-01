package aws

import (
	"fmt"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/aws/aws-sdk-go/service/ecs" // Import ECS package
	"github.com/aws/aws-sdk-go/service/elbv2"
	"github.com/aws/aws-sdk-go/service/s3"
	"github.com/rs/zerolog/log" // Import zerolog's global logger
)

type AWSClient struct {
	ec2Client         *ec2.EC2
	s3Client          *s3.S3 // Default S3 client (for ListBuckets, etc.)
	elbClient         *elbv2.ELBV2
	ecsClient         *ecs.ECS // Add ECS client
	region            string
	sess              *session.Session  // Store the session
	s3ClientsByRegion map[string]*s3.S3 // Cache for regional S3 clients
	s3ClientMutex     sync.RWMutex      // Mutex for the cache
}

// BucketInfo holds information about an S3 bucket, including its region.
type BucketInfo struct {
	Name         *string
	Region       string
	CreationDate *time.Time // Corrected type
}

// NewAWSClient initializes a new AWS client for the specified region.
// It expects AWS credentials to be configured via environment variables or IAM role.
func NewAWSClient(region string) (*AWSClient, error) {
	sess, err := session.NewSession(&aws.Config{
		Region: aws.String(region)},
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create session: %w", err)
	}

	return &AWSClient{
		ec2Client:         ec2.New(sess),
		s3Client:          s3.New(sess), // Default client uses the primary region
		elbClient:         elbv2.New(sess),
		ecsClient:         ecs.New(sess), // Initialize ECS client
		region:            region,
		sess:              sess,                    // Store session
		s3ClientsByRegion: make(map[string]*s3.S3), // Initialize cache
	}, nil
}

// getS3ClientForRegion returns an S3 client configured for the specified region.
// It uses a cache to avoid recreating clients.
func (c *AWSClient) getS3ClientForRegion(region string) (*s3.S3, error) {
	// Check if client for this region is already cached (Read Lock)
	c.s3ClientMutex.RLock()
	regionalClient, ok := c.s3ClientsByRegion[region]
	c.s3ClientMutex.RUnlock()

	if ok {
		return regionalClient, nil
	}

	// Client not cached, create a new one (Write Lock)
	c.s3ClientMutex.Lock()
	defer c.s3ClientMutex.Unlock()

	// Double-check if another goroutine created it while waiting for the lock
	regionalClient, ok = c.s3ClientsByRegion[region]
	if ok {
		return regionalClient, nil
	}

	// Create new session and client for the specific region
	regionalSess, err := session.NewSession(c.sess.Config.Copy(&aws.Config{Region: aws.String(region)}))
	if err != nil {
		return nil, fmt.Errorf("failed to create session for region %s: %w", region, err)
	}
	regionalClient = s3.New(regionalSess)

	// Cache the new client
	c.s3ClientsByRegion[region] = regionalClient

	return regionalClient, nil
}

// ListEC2Instances retrieves a list of EC2 instances.
func (c *AWSClient) ListEC2Instances() ([]*ec2.Instance, error) {
	// log.Debug().Msg("Listing EC2 instances") // Example debug log
	var instances []*ec2.Instance
	input := &ec2.DescribeInstancesInput{}

	err := c.ec2Client.DescribeInstancesPages(input,
		func(page *ec2.DescribeInstancesOutput, lastPage bool) bool {
			for _, reservation := range page.Reservations {
				instances = append(instances, reservation.Instances...)
			}
			return !lastPage // Continue paging if not the last page
		})

	if err != nil {
		log.Error().Err(err).Msg("Failed to describe EC2 instances")
		return nil, fmt.Errorf("failed to describe EC2 instances: %w", err)
	}
	return instances, nil
}

// ListVolumes retrieves a list of EBS volumes.
func (c *AWSClient) ListVolumes() ([]*ec2.Volume, error) {
	// log.Debug().Msg("Listing EBS volumes")
	var volumes []*ec2.Volume
	input := &ec2.DescribeVolumesInput{}

	err := c.ec2Client.DescribeVolumesPages(input,
		func(page *ec2.DescribeVolumesOutput, lastPage bool) bool {
			volumes = append(volumes, page.Volumes...)
			return !lastPage // Continue paging
		})

	if err != nil {
		log.Error().Err(err).Msg("Failed to describe EBS volumes")
		return nil, fmt.Errorf("failed to describe EBS volumes: %w", err)
	}
	return volumes, nil
}

// ListVPCs retrieves a list of VPCs.
func (c *AWSClient) ListVPCs() ([]*ec2.Vpc, error) {
	// log.Debug().Msg("Listing VPCs")
	var vpcs []*ec2.Vpc
	input := &ec2.DescribeVpcsInput{}

	err := c.ec2Client.DescribeVpcsPages(input,
		func(page *ec2.DescribeVpcsOutput, lastPage bool) bool {
			vpcs = append(vpcs, page.Vpcs...)
			return !lastPage // Continue paging
		})

	if err != nil {
		log.Error().Err(err).Msg("Failed to describe VPCs")
		return nil, fmt.Errorf("failed to describe VPCs: %w", err)
	}
	return vpcs, nil
}

// ListElasticIPs retrieves a list of Elastic IPs.
func (c *AWSClient) ListElasticIPs() ([]*ec2.Address, error) {
	// log.Debug().Msg("Listing Elastic IPs")
	input := &ec2.DescribeAddressesInput{}
	result, err := c.ec2Client.DescribeAddresses(input)
	if err != nil {
		log.Error().Err(err).Msg("Failed to describe Elastic IPs")
		return nil, fmt.Errorf("failed to describe Elastic IPs: %w", err)
	}
	return result.Addresses, nil
}

// ListNatGateways retrieves a list of NAT Gateways.
func (c *AWSClient) ListNatGateways() ([]*ec2.NatGateway, error) {
	var natGateways []*ec2.NatGateway
	input := &ec2.DescribeNatGatewaysInput{}

	err := c.ec2Client.DescribeNatGatewaysPages(input,
		func(page *ec2.DescribeNatGatewaysOutput, lastPage bool) bool {
			natGateways = append(natGateways, page.NatGateways...)
			return !lastPage // Continue paging
		})

	if err != nil {
		log.Error().Err(err).Msg("Failed to describe NAT Gateways")
		return nil, fmt.Errorf("failed to describe NAT Gateways: %w", err)
	}
	return natGateways, nil
}

// ListInternetGateways retrieves a list of Internet Gateways.
func (c *AWSClient) ListInternetGateways() ([]*ec2.InternetGateway, error) {
	var internetGateways []*ec2.InternetGateway
	input := &ec2.DescribeInternetGatewaysInput{}

	err := c.ec2Client.DescribeInternetGatewaysPages(input,
		func(page *ec2.DescribeInternetGatewaysOutput, lastPage bool) bool {
			internetGateways = append(internetGateways, page.InternetGateways...)
			return !lastPage // Continue paging
		})

	if err != nil {
		log.Error().Err(err).Msg("Failed to describe Internet Gateways")
		return nil, fmt.Errorf("failed to describe Internet Gateways: %w", err)
	}
	return internetGateways, nil
}

// ListECSClusters retrieves a list of ECS Clusters.
func (c *AWSClient) ListECSClusters() ([]*ecs.Cluster, error) {
	var clusterArns []*string
	listInput := &ecs.ListClustersInput{}

	// List all cluster ARNs first
	err := c.ecsClient.ListClustersPages(listInput,
		func(page *ecs.ListClustersOutput, lastPage bool) bool {
			clusterArns = append(clusterArns, page.ClusterArns...)
			return !lastPage // Continue paging
		})

	if err != nil {
		log.Error().Err(err).Msg("Failed to list ECS Clusters")
		return nil, fmt.Errorf("failed to list ECS Clusters: %w", err)
	}

	if len(clusterArns) == 0 {
		return []*ecs.Cluster{}, nil // No clusters found
	}

	// Describe the clusters to get details including tags
	// DescribeClusters can take up to 100 ARNs at a time
	var clusters []*ecs.Cluster
	chunkSize := 100
	for i := 0; i < len(clusterArns); i += chunkSize {
		end := i + chunkSize
		if end > len(clusterArns) {
			end = len(clusterArns)
		}
		chunk := clusterArns[i:end]

		describeInput := &ecs.DescribeClustersInput{
			Clusters: chunk,
			Include:  []*string{aws.String("TAGS")}, // Ensure tags are included
		}

		result, err := c.ecsClient.DescribeClusters(describeInput)
		if err != nil {
			// Log error for the chunk but try to continue
			log.Error().Err(err).Int("chunkStart", i).Int("chunkEnd", end).Msg("Failed to describe ECS Cluster chunk")
			continue
		}
		clusters = append(clusters, result.Clusters...)

		// Handle failures reported in the output
		for _, failure := range result.Failures {
			log.Warn().Str("arn", *failure.Arn).Str("reason", *failure.Reason).Msg("Failed to describe specific ECS Cluster")
		}
	}

	return clusters, nil
}

// ListLoadBalancers retrieves a list of ELBv2 Load Balancers.
func (c *AWSClient) ListLoadBalancers() ([]*elbv2.LoadBalancer, error) {
	// log.Debug().Msg("Listing Load Balancers (v2)")
	var loadBalancers []*elbv2.LoadBalancer
	input := &elbv2.DescribeLoadBalancersInput{}

	err := c.elbClient.DescribeLoadBalancersPages(input,
		func(page *elbv2.DescribeLoadBalancersOutput, lastPage bool) bool {
			loadBalancers = append(loadBalancers, page.LoadBalancers...)
			return !lastPage // Continue paging
		})

	if err != nil {
		log.Error().Err(err).Msg("Failed to describe Load Balancers")
		return nil, fmt.Errorf("failed to describe Load Balancers: %w", err)
	}
	return loadBalancers, nil
}

// GetLoadBalancerTags retrieves tags for a specific ELBv2 Load Balancer.
func (c *AWSClient) GetLoadBalancerTags(lbArn *string) ([]*elbv2.Tag, error) {
	// log.Debug().Str("arn", *lbArn).Msg("Getting tags for Load Balancer")
	input := &elbv2.DescribeTagsInput{
		ResourceArns: []*string{lbArn},
	}

	result, err := c.elbClient.DescribeTags(input)
	if err != nil {
		// Check for specific errors like LoadBalancerNotFound
		if aerr, ok := err.(awserr.Error); ok {
			log.Warn().Err(aerr).Str("arn", *lbArn).Str("code", aerr.Code()).Msg("AWS error getting tags for LB")
		} else {
			log.Warn().Err(err).Str("arn", *lbArn).Msg("Non-AWS error getting tags for LB")
		}
		// Still return an error for the caller in main.go to handle
		return nil, fmt.Errorf("failed to get tags for Load Balancer %s: %w", *lbArn, err)
	}

	// DescribeTags returns a list of TagDescriptions. We expect only one for the given ARN.
	if len(result.TagDescriptions) > 0 {
		return result.TagDescriptions[0].Tags, nil
	}

	// No tags found is not an error in this context, return empty slice.
	return []*elbv2.Tag{}, nil
}

// ListS3Buckets retrieves a list of S3 buckets and their creation dates.
func (c *AWSClient) ListS3Buckets() ([]BucketInfo, error) {
	// log.Debug().Msg("Listing S3 buckets")
	listResult, err := c.s3Client.ListBuckets(nil) // Use default client for listing
	if err != nil {
		log.Error().Err(err).Msg("Failed to list S3 buckets")
		return nil, fmt.Errorf("failed to list S3 buckets: %w", err)
	}

	var bucketInfos []BucketInfo
	var wg sync.WaitGroup
	var mu sync.Mutex // Mutex to protect bucketInfos slice

	for _, bucket := range listResult.Buckets {
		if bucket == nil || bucket.Name == nil {
			continue
		}
		wg.Add(1)
		go func(b *s3.Bucket) {
			defer wg.Done()
			locInput := &s3.GetBucketLocationInput{Bucket: b.Name}
			var region string = "us-east-1" // Default region

			// Use the default client to get location initially
			locOutput, err := c.s3Client.GetBucketLocation(locInput)
			if err != nil {
				log.Warn().Err(err).Str("bucket", *b.Name).Msg("Could not get location for bucket. Tagging might fail.")
				// Assuming the client's primary region if location fails.
				region = c.region
			} else if locOutput.LocationConstraint != nil {
				region = *locOutput.LocationConstraint
			}
			// The AWS SDK for Go might return "EU" for eu-west-1, handle this common case.
			if region == "EU" {
				region = "eu-west-1"
			}

			mu.Lock()
			bucketInfos = append(bucketInfos, BucketInfo{
				Name:         b.Name,
				Region:       region,
				CreationDate: b.CreationDate,
			})
			mu.Unlock()
		}(bucket)
	}

	wg.Wait()
	return bucketInfos, nil
}

// GetS3BucketTags retrieves tags for a specific S3 bucket using a client configured for the bucket's region.
func (c *AWSClient) GetS3BucketTags(bucketName *string, bucketRegion string) (map[string]string, error) {
	// log.Debug().Str("bucket", *bucketName).Str("region", bucketRegion).Msg("Getting tags for S3 bucket")
	// Get the appropriate regional client
	regionalClient, err := c.getS3ClientForRegion(bucketRegion)
	if err != nil {
		log.Error().Err(err).Str("region", bucketRegion).Msg("Failed to get S3 client for region")
		return nil, fmt.Errorf("failed to get S3 client for bucket %s in region %s: %w", *bucketName, bucketRegion, err)
	}

	input := &s3.GetBucketTaggingInput{
		Bucket: bucketName,
	}
	result, err := regionalClient.GetBucketTagging(input) // Use regional client
	// Handle cases where the bucket might not have tags
	if err != nil {
		if aerr, ok := err.(awserr.Error); ok {
			// Use string literal for the error code
			if aerr.Code() == "NoSuchTagSet" {
				// log.Debug().Str("bucket", *bucketName).Str("region", bucketRegion).Msg("Bucket has no tags (NoSuchTagSet)")
				return map[string]string{}, nil // No tags is not an error
			}
			// Log other AWS errors
			log.Warn().Err(aerr).Str("bucket", *bucketName).Str("region", bucketRegion).Str("code", aerr.Code()).Msg("AWS error getting tags for bucket")
		} else {
			// Log non-AWS errors
			log.Warn().Err(err).Str("bucket", *bucketName).Str("region", bucketRegion).Msg("Non-AWS error getting tags for bucket")
		}
		// Still return error for the caller in main.go
		return nil, fmt.Errorf("failed to get tags for bucket %s in region %s: %w", *bucketName, bucketRegion, err)
	}

	tags := make(map[string]string)
	for _, tag := range result.TagSet {
		if tag.Key != nil && tag.Value != nil {
			tags[*tag.Key] = *tag.Value
		}
	}
	return tags, nil
}

// ConvertEC2Tags converts EC2 tags ( []*ec2.Tag ) to a map[string]string for consistent display
func ConvertEC2Tags(ec2Tags []*ec2.Tag) map[string]string {
	tags := make(map[string]string)
	for _, t := range ec2Tags {
		if t.Key != nil && t.Value != nil {
			tags[*t.Key] = *t.Value
		}
	}
	return tags
}

// ConvertELBTags converts ELBv2 tags ( []*elbv2.Tag ) to a map[string]string
func ConvertELBTags(elbTags []*elbv2.Tag) map[string]string {
	tags := make(map[string]string)
	for _, t := range elbTags {
		if t.Key != nil && t.Value != nil {
			tags[*t.Key] = *t.Value
		}
	}
	return tags
}

// ConvertECSTags converts ECS tags ( []*ecs.Tag ) to a map[string]string
func ConvertECSTags(ecsTags []*ecs.Tag) map[string]string {
	tags := make(map[string]string)
	for _, t := range ecsTags {
		if t.Key != nil && t.Value != nil {
			tags[*t.Key] = *t.Value
		}
	}
	return tags
}

// ApplyTagsToEC2Resource applies tags to EC2 resources (Instance, Volume, VPC, NAT Gateway, IGW, EIP).
func (c *AWSClient) ApplyTagsToEC2Resource(resourceID string, tags map[string]string) error {
	log.Debug().Str("resourceId", resourceID).Interface("tags", tags).Msg("Applying tags to EC2 resource")

	awsTags := []*ec2.Tag{}
	for k, v := range tags {
		awsTags = append(awsTags, &ec2.Tag{
			Key:   aws.String(k),
			Value: aws.String(v),
		})
	}

	input := &ec2.CreateTagsInput{
		Resources: []*string{aws.String(resourceID)},
		Tags:      awsTags,
	}

	_, err := c.ec2Client.CreateTags(input)
	if err != nil {
		log.Error().Err(err).Str("resourceId", resourceID).Msg("Failed to apply tags to EC2 resource")
		return fmt.Errorf("failed to apply tags to EC2 resource %s: %w", resourceID, err)
	}
	log.Info().Str("resourceId", resourceID).Msg("Successfully applied tags to EC2 resource")
	return nil
}

// ApplyTagsToELBv2Resource applies tags to ELBv2 resources (Load Balancers).
func (c *AWSClient) ApplyTagsToELBv2Resource(resourceARN string, tags map[string]string) error {
	log.Debug().Str("resourceArn", resourceARN).Interface("tags", tags).Msg("Applying tags to ELBv2 resource")

	awsTags := []*elbv2.Tag{}
	for k, v := range tags {
		awsTags = append(awsTags, &elbv2.Tag{
			Key:   aws.String(k),
			Value: aws.String(v),
		})
	}

	input := &elbv2.AddTagsInput{
		ResourceArns: []*string{aws.String(resourceARN)},
		Tags:         awsTags,
	}

	_, err := c.elbClient.AddTags(input)
	if err != nil {
		log.Error().Err(err).Str("resourceArn", resourceARN).Msg("Failed to apply tags to ELBv2 resource")
		return fmt.Errorf("failed to apply tags to ELBv2 resource %s: %w", resourceARN, err)
	}
	log.Info().Str("resourceArn", resourceARN).Msg("Successfully applied tags to ELBv2 resource")
	return nil
}

// ApplyTagsToECSResource applies tags to ECS resources (Clusters).
func (c *AWSClient) ApplyTagsToECSResource(resourceARN string, tags map[string]string) error {
	log.Debug().Str("resourceArn", resourceARN).Interface("tags", tags).Msg("Applying tags to ECS resource")

	awsTags := []*ecs.Tag{}
	for k, v := range tags {
		awsTags = append(awsTags, &ecs.Tag{
			Key:   aws.String(k),
			Value: aws.String(v),
		})
	}

	input := &ecs.TagResourceInput{
		ResourceArn: aws.String(resourceARN),
		Tags:        awsTags,
	}

	_, err := c.ecsClient.TagResource(input)
	if err != nil {
		log.Error().Err(err).Str("resourceArn", resourceARN).Msg("Failed to apply tags to ECS resource")
		return fmt.Errorf("failed to apply tags to ECS resource %s: %w", resourceARN, err)
	}
	log.Info().Str("resourceArn", resourceARN).Msg("Successfully applied tags to ECS resource")
	return nil
}

// ApplyTagsToS3Bucket applies tags to an S3 bucket.
// NOTE: This replaces ALL existing tags on the bucket. It fetches existing tags first and merges the new ones.
func (c *AWSClient) ApplyTagsToS3Bucket(bucketName string, bucketRegion string, tagsToApply map[string]string) error {
	log.Debug().Str("bucket", bucketName).Str("region", bucketRegion).Interface("tags", tagsToApply).Msg("Applying tags to S3 bucket")

	// Get the appropriate regional client
	regionalClient, err := c.getS3ClientForRegion(bucketRegion)
	if err != nil {
		log.Error().Err(err).Str("region", bucketRegion).Msg("Failed to get S3 client for region")
		return fmt.Errorf("failed to get S3 client for bucket %s in region %s: %w", bucketName, bucketRegion, err)
	}

	// 1. Get existing tags
	existingTags := make(map[string]string)
	getInput := &s3.GetBucketTaggingInput{Bucket: aws.String(bucketName)}
	getResult, err := regionalClient.GetBucketTagging(getInput)

	if err != nil {
		if aerr, ok := err.(awserr.Error); ok && aerr.Code() == "NoSuchTagSet" {
			log.Debug().Str("bucket", bucketName).Msg("Bucket has no existing tags (NoSuchTagSet).")
			// No existing tags, proceed with an empty map
		} else {
			// Any other error while getting tags is fatal for this operation
			log.Error().Err(err).Str("bucket", bucketName).Msg("Failed to get existing tags for S3 bucket")
			return fmt.Errorf("failed to get existing tags for bucket %s: %w", bucketName, err)
		}
	} else {
		// Convert existing tags to map
		for _, tag := range getResult.TagSet {
			if tag.Key != nil && tag.Value != nil {
				existingTags[*tag.Key] = *tag.Value
			}
		}
		log.Debug().Str("bucket", bucketName).Interface("existingTags", existingTags).Msg("Fetched existing S3 tags")
	}

	// 2. Merge new/updated tags
	mergedTags := make(map[string]string)
	for k, v := range existingTags { // Copy existing
		mergedTags[k] = v
	}
	for k, v := range tagsToApply { // Add/overwrite with new ones
		mergedTags[k] = v
	}
	log.Debug().Str("bucket", bucketName).Interface("mergedTags", mergedTags).Msg("Merged S3 tags")

	// 3. Convert merged map back to S3 TagSet format
	newTagSet := []*s3.Tag{}
	for k, v := range mergedTags {
		newTagSet = append(newTagSet, &s3.Tag{
			Key:   aws.String(k),
			Value: aws.String(v),
		})
	}

	// 4. Apply the merged tag set
	putInput := &s3.PutBucketTaggingInput{
		Bucket: aws.String(bucketName),
		Tagging: &s3.Tagging{
			TagSet: newTagSet,
		},
	}

	_, err = regionalClient.PutBucketTagging(putInput)
	if err != nil {
		log.Error().Err(err).Str("bucket", bucketName).Msg("Failed to apply tags to S3 bucket")
		return fmt.Errorf("failed to apply tags to S3 bucket %s: %w", bucketName, err)
	}

	log.Info().Str("bucket", bucketName).Msg("Successfully applied tags to S3 bucket")
	return nil
}
