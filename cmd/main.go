package main

import (
	"bufio" // For interactive prompt
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strconv" // For parsing user choice
	"strings"
	"time"

	"aws-resource-lister/internal/ai"
	"aws-resource-lister/internal/aws"
	"aws-resource-lister/internal/config"
	"aws-resource-lister/internal/policy"
	"aws-resource-lister/internal/recommendation" // New package

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

// ResourceInfo defines the structure for JSON output
type ResourceInfo struct {
	Kind   string                   `json:"kind"`
	Name   string                   `json:"name"`
	Region string                   `json:"region"`
	Tags   map[string]string        `json:"tags"`
	Issues []policy.ValidationIssue `json:"issues,omitempty"` // Issues now contain recommendations
}

// Helper function to convert tags map to lowercase
func lowercaseTags(tags map[string]string) map[string]string {
	lowerTags := make(map[string]string, len(tags))
	for k, v := range tags {
		lowerTags[strings.ToLower(k)] = strings.ToLower(v)
	}
	return lowerTags
}

// Global flags
var (
	logLevelFlag = flag.String("log-level", "info", "Set log level (trace, debug, info, warn, error, fatal, panic)")
)

func main() {
	// Parse global flags first
	flag.Parse()

	// Configure zerolog for console output to stderr
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr})
	// Set global log level based on the flag
	level, err := zerolog.ParseLevel(*logLevelFlag)
	if err != nil {
		log.Warn().Str("level", *logLevelFlag).Msg("Invalid log level specified, defaulting to 'info'")
		level = zerolog.InfoLevel
	}
	zerolog.SetGlobalLevel(level) // Set level from flag

	log.Info().Str("level", level.String()).Msg("Log level set") // Log the effective level

	// Determine subcommand
	command := "analyze" // Default command changed to analyze
	if flag.NArg() > 0 {
		command = flag.Arg(0)
	}

	// Load base configuration (needed by both commands for credentials)
	cfg, err := config.LoadConfig()
	if err != nil {
		log.Warn().Err(err).Msg("Failed to load .env file. Relying on environment variables or IAM role.")
		cfg = &config.Config{ // Initialize empty config if .env fails but env vars might exist
			AWSRegion:          config.GetEnv("AWS_REGION", ""),
			AWSAccessKeyID:     config.GetEnv("AWS_ACCESS_KEY_ID", ""),
			AWSSecretAccessKey: config.GetEnv("AWS_SECRET_ACCESS_KEY", ""),
			AIAPIURL:           config.GetEnv("AI_API_URL", ""),
			AIAPIToken:         config.GetEnv("AI_API_TOKEN", ""),
		}
	}

	// Execute command
	switch command {
	case "analyze": // Renamed from list
		// Define flags specific to the analyze command
		analyzeFlags := flag.NewFlagSet("analyze", flag.ExitOnError) // Renamed from listFlags
		regionFlag := analyzeFlags.String("region", "", "Comma-separated list of AWS regions to target (overrides environment/config file)")
		lowercaseFlag := analyzeFlags.Bool("lowercase", false, "Convert all output strings (kind, name, region, tags) to lowercase")
		policyFileFlag := analyzeFlags.String("policy", "", "Path to the tag policy JSON file")
		aiModelFlag := analyzeFlags.String("ai-model", "", "Enable AI recommendations using the specified model name (e.g., 'deepseek-r1:1.5b'). Requires AI_API_URL and AI_API_TOKEN env vars.") // Updated description, default empty
		aiTimeoutFlag := analyzeFlags.Duration("ai-timeout", 20*time.Second, "Timeout duration for AI API requests (e.g., 30s, 1m)")
		inputJsonFlag := analyzeFlags.String("input-json", "", "Path to a JSON file containing pre-fetched resource data (skips AWS fetching)")

		// Parse arguments after the command name
		analyzeFlags.Parse(flag.Args()[1:]) // Renamed from listFlags

		runAnalyzeCommand(cfg, regionFlag, lowercaseFlag, policyFileFlag, aiModelFlag, aiTimeoutFlag, inputJsonFlag) // Pass aiModelFlag directly
	case "apply":
		// Define flags specific to the apply command
		applyFlags := flag.NewFlagSet("apply", flag.ExitOnError)
		inputFlag := applyFlags.String("input", "", "Path to the JSON file containing resources and recommendations (required)")
		kindFlag := applyFlags.String("kind", "", "Comma-separated list of resource kinds to apply changes to (e.g., ec2,s3). If empty, applies to all.") // Updated description

		// Parse arguments after the command name
		applyFlags.Parse(flag.Args()[1:])

		if *inputFlag == "" {
			log.Fatal().Msg("-input flag is required for the apply command")
		}

		runApplyCommand(cfg, inputFlag, kindFlag) // Pass kindFlag
	default:
		log.Fatal().Str("command", command).Msg("Unknown command. Available commands: analyze, apply") // Updated error message
	}
}

// runAnalyzeCommand contains the logic previously in main for listing/analyzing resources.
func runAnalyzeCommand(cfg *config.Config, regionFlag *string, lowercaseFlag *bool, policyFileFlag *string, aiModelFlag *string, aiTimeoutFlag *time.Duration, inputJsonFlag *string) { // aiRecommendationsFlag removed
	log.Info().Msg("Running analyze command...") // Updated log message

	// Determine if AI recommendations are enabled based on aiModelFlag
	aiRecommendationsEnabled := *aiModelFlag != ""

	// Validate AI config if enabled
	if aiRecommendationsEnabled {
		if cfg.AIAPIURL == "" {
			log.Fatal().Msg("AI recommendations enabled via -ai-model, but AI_API_URL environment variable is not set.")
		}
		if cfg.AIAPIToken == "" {
			log.Fatal().Msg("AI recommendations enabled via -ai-model, but AI_API_TOKEN environment variable is not set.")
		}
		log.Info().Str("url", cfg.AIAPIURL).Str("model", *aiModelFlag).Msg("AI Recommendations enabled")
	} else {
		log.Info().Msg("AI Recommendations disabled (no model specified via -ai-model)")
	}

	// Load Tag Policy
	tagPolicy, err := policy.LoadPolicy(*policyFileFlag)
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to load tag policy")
	}

	// Slice to hold all resource information
	var allResources []ResourceInfo

	// --- Check if reading from input JSON or fetching from AWS ---
	if *inputJsonFlag != "" {
		log.Info().Str("file", *inputJsonFlag).Msg("Reading resource data from input JSON file, skipping AWS fetch.")
		jsonData, err := os.ReadFile(*inputJsonFlag)
		if err != nil {
			log.Fatal().Err(err).Str("file", *inputJsonFlag).Msg("Failed to read input JSON file")
		}

		err = json.Unmarshal(jsonData, &allResources)
		if err != nil {
			log.Fatal().Err(err).Str("file", *inputJsonFlag).Msg("Failed to unmarshal JSON data from input file")
		}
		log.Info().Int("count", len(allResources)).Str("file", *inputJsonFlag).Msg("Successfully loaded resources from JSON file")

	} else {
		log.Info().Msg("Fetching resource data from AWS.")
		// --- Fetch from AWS ---

		// Determine target regions (only needed if fetching from AWS)
		var targetRegions []string
		if *regionFlag != "" {
			targetRegions = strings.Split(*regionFlag, ",")
			log.Info().Strs("regions", targetRegions).Msg("Using regions from -region flag")
		} else if cfg.AWSRegion != "" {
			targetRegions = []string{cfg.AWSRegion}
			log.Info().Str("region", cfg.AWSRegion).Msg("Using region from environment/config")
		} else {
			log.Fatal().Msg("AWS region must be set via -region flag, AWS_REGION environment variable, or in .env file when not using -input-json.")
		}

		// --- Resource Fetching Loops ---
		for _, currentRegion := range targetRegions {
			currentRegion = strings.TrimSpace(currentRegion) // Clean up spaces
			if currentRegion == "" {
				continue // Skip empty region strings
			}

			log.Info().Str("region", currentRegion).Msg("Processing region")

			// Initialize AWS client for the current region
			client, err := aws.NewAWSClient(currentRegion)
			if err != nil {
				log.Error().Err(err).Str("region", currentRegion).Msg("Failed to create AWS client for region. Skipping.")
				continue
			}

			// --- EC2 Instances ---
			log.Info().Str("region", currentRegion).Msg("Fetching EC2 Instances...")
			instances, err := client.ListEC2Instances()
			if err != nil {
				log.Error().Err(err).Str("region", currentRegion).Msg("Failed to list EC2 instances")
			} else {
				log.Info().Int("count", len(instances)).Str("region", currentRegion).Msg("Fetched EC2 Instances")
				for _, instance := range instances {
					if instance != nil && instance.InstanceId != nil {
						tags := aws.ConvertEC2Tags(instance.Tags)
						kind := "ec2"
						name := *instance.InstanceId
						region := currentRegion
						if *lowercaseFlag {
							kind = strings.ToLower(kind)
							name = strings.ToLower(name)
							region = strings.ToLower(region)
							tags = lowercaseTags(tags)
						}
						issues := policy.ValidateResource(kind, name, region, tags, tagPolicy, *lowercaseFlag)
						resInfo := ResourceInfo{ // Create struct but don't add AI recs yet
							Kind:   kind,
							Name:   name,
							Region: region,
							Tags:   tags,
							Issues: issues,
						}
						allResources = append(allResources, resInfo) // Add to the main slice
					}
				}
			}

			// --- EBS Volumes ---
			log.Info().Str("region", currentRegion).Msg("Fetching EBS Volumes...")
			volumes, err := client.ListVolumes()
			if err != nil {
				log.Error().Err(err).Str("region", currentRegion).Msg("Failed to list volumes")
			} else {
				log.Info().Int("count", len(volumes)).Str("region", currentRegion).Msg("Fetched EBS Volumes")
				for _, volume := range volumes {
					if volume != nil && volume.VolumeId != nil {
						tags := aws.ConvertEC2Tags(volume.Tags)
						kind := "ebs"
						name := *volume.VolumeId
						region := currentRegion
						if *lowercaseFlag {
							kind = strings.ToLower(kind)
							name = strings.ToLower(name)
							region = strings.ToLower(region)
							tags = lowercaseTags(tags)
						}
						issues := policy.ValidateResource(kind, name, region, tags, tagPolicy, *lowercaseFlag)
						resInfo := ResourceInfo{
							Kind:   kind,
							Name:   name,
							Region: region,
							Tags:   tags,
							Issues: issues,
						}
						allResources = append(allResources, resInfo)
					}
				}
			}

			// --- VPCs ---
			log.Info().Str("region", currentRegion).Msg("Fetching VPCs...")
			vpcs, err := client.ListVPCs()
			if err != nil {
				log.Error().Err(err).Str("region", currentRegion).Msg("Failed to list VPCs")
			} else {
				log.Info().Int("count", len(vpcs)).Str("region", currentRegion).Msg("Fetched VPCs")
				for _, vpc := range vpcs {
					if vpc != nil && vpc.VpcId != nil {
						tags := aws.ConvertEC2Tags(vpc.Tags)
						kind := "vpc"
						name := *vpc.VpcId
						region := currentRegion
						if *lowercaseFlag {
							kind = strings.ToLower(kind)
							name = strings.ToLower(name)
							region = strings.ToLower(region)
							tags = lowercaseTags(tags)
						}
						issues := policy.ValidateResource(kind, name, region, tags, tagPolicy, *lowercaseFlag)
						resInfo := ResourceInfo{
							Kind:   kind,
							Name:   name,
							Region: region,
							Tags:   tags,
							Issues: issues,
						}
						allResources = append(allResources, resInfo)
					}
				}
			}

			// --- NAT Gateways ---
			log.Info().Str("region", currentRegion).Msg("Fetching NAT Gateways...")
			natGateways, err := client.ListNatGateways()
			if err != nil {
				log.Error().Err(err).Str("region", currentRegion).Msg("Failed to list NAT Gateways")
			} else {
				log.Info().Int("count", len(natGateways)).Str("region", currentRegion).Msg("Fetched NAT Gateways")
				for _, ngw := range natGateways {
					if ngw != nil && ngw.NatGatewayId != nil {
						tags := aws.ConvertEC2Tags(ngw.Tags)
						kind := "natgateway"
						name := *ngw.NatGatewayId
						region := currentRegion
						if *lowercaseFlag {
							kind = strings.ToLower(kind)
							name = strings.ToLower(name)
							region = strings.ToLower(region)
							tags = lowercaseTags(tags)
						}
						issues := policy.ValidateResource(kind, name, region, tags, tagPolicy, *lowercaseFlag)
						resInfo := ResourceInfo{
							Kind:   kind,
							Name:   name,
							Region: region,
							Tags:   tags,
							Issues: issues,
						}
						allResources = append(allResources, resInfo)
					}
				}
			}

			// --- Internet Gateways ---
			log.Info().Str("region", currentRegion).Msg("Fetching Internet Gateways...")
			internetGateways, err := client.ListInternetGateways()
			if err != nil {
				log.Error().Err(err).Str("region", currentRegion).Msg("Failed to list Internet Gateways")
			} else {
				log.Info().Int("count", len(internetGateways)).Str("region", currentRegion).Msg("Fetched Internet Gateways")
				for _, igw := range internetGateways {
					if igw != nil && igw.InternetGatewayId != nil {
						tags := aws.ConvertEC2Tags(igw.Tags)
						kind := "internetgateway"
						name := *igw.InternetGatewayId
						region := currentRegion
						if *lowercaseFlag {
							kind = strings.ToLower(kind)
							name = strings.ToLower(name)
							region = strings.ToLower(region)
							tags = lowercaseTags(tags)
						}
						issues := policy.ValidateResource(kind, name, region, tags, tagPolicy, *lowercaseFlag)
						resInfo := ResourceInfo{
							Kind:   kind,
							Name:   name,
							Region: region,
							Tags:   tags,
							Issues: issues,
						}
						allResources = append(allResources, resInfo)
					}
				}
			}

			// --- Elastic IPs ---
			log.Info().Str("region", currentRegion).Msg("Fetching Elastic IPs...")
			elasticIPs, err := client.ListElasticIPs()
			if err != nil {
				log.Error().Err(err).Str("region", currentRegion).Msg("Failed to list Elastic IPs")
			} else {
				log.Info().Int("count", len(elasticIPs)).Str("region", currentRegion).Msg("Fetched Elastic IPs")
				for _, ip := range elasticIPs {
					if ip != nil && ip.AllocationId != nil {
						tags := aws.ConvertEC2Tags(ip.Tags)
						kind := "eip"
						name := *ip.AllocationId
						region := currentRegion
						if *lowercaseFlag {
							kind = strings.ToLower(kind)
							name = strings.ToLower(name)
							region = strings.ToLower(region)
							tags = lowercaseTags(tags)
						}
						issues := policy.ValidateResource(kind, name, region, tags, tagPolicy, *lowercaseFlag)
						resInfo := ResourceInfo{
							Kind:   kind,
							Name:   name,
							Region: region,
							Tags:   tags,
							Issues: issues,
						}
						allResources = append(allResources, resInfo)
					}
				}
			}

			// --- Load Balancers (ELBv2) ---
			log.Info().Str("region", currentRegion).Msg("Fetching Load Balancers (v2)...")
			loadBalancers, err := client.ListLoadBalancers()
			if err != nil {
				log.Error().Err(err).Str("region", currentRegion).Msg("Failed to list load balancers")
			} else {
				log.Info().Int("count", len(loadBalancers)).Str("region", currentRegion).Msg("Fetched Load Balancers (v2)")
				for _, lb := range loadBalancers {
					if lb != nil && lb.LoadBalancerArn != nil {
						elbTags, err := client.GetLoadBalancerTags(lb.LoadBalancerArn)
						tags := aws.ConvertELBTags(elbTags)
						if err != nil {
							log.Warn().Str("arn", *lb.LoadBalancerArn).Str("region", currentRegion).Msg("Using empty tags for LB due to previous error")
							tags = make(map[string]string)
						}
						kind := "elbv2"
						name := *lb.LoadBalancerArn
						region := currentRegion
						if *lowercaseFlag {
							kind = strings.ToLower(kind)
							name = strings.ToLower(name)
							region = strings.ToLower(region)
							tags = lowercaseTags(tags)
						}
						issues := policy.ValidateResource(kind, name, region, tags, tagPolicy, *lowercaseFlag)
						resInfo := ResourceInfo{
							Kind:   kind,
							Name:   name,
							Region: region,
							Tags:   tags,
							Issues: issues,
						}
						allResources = append(allResources, resInfo)
					}
				}
			}

			// --- ECS Clusters ---
			log.Info().Str("region", currentRegion).Msg("Fetching ECS Clusters...")
			ecsClusters, err := client.ListECSClusters()
			if err != nil {
				log.Error().Err(err).Str("region", currentRegion).Msg("Failed to list ECS Clusters")
			} else {
				log.Info().Int("count", len(ecsClusters)).Str("region", currentRegion).Msg("Fetched ECS Clusters")
				for _, cluster := range ecsClusters {
					if cluster != nil && cluster.ClusterArn != nil {
						tags := aws.ConvertECSTags(cluster.Tags)
						kind := "ecscluster"
						name := *cluster.ClusterArn
						region := currentRegion
						if *lowercaseFlag {
							kind = strings.ToLower(kind)
							name = strings.ToLower(name)
							region = strings.ToLower(region)
							tags = lowercaseTags(tags)
						}
						issues := policy.ValidateResource(kind, name, region, tags, tagPolicy, *lowercaseFlag)
						resInfo := ResourceInfo{
							Kind:   kind,
							Name:   name,
							Region: region,
							Tags:   tags,
							Issues: issues,
						}
						allResources = append(allResources, resInfo)
					}
				}
			}

		} // --- End of region loop ---

		// --- S3 Buckets ---
		log.Info().Msg("Fetching S3 Buckets (Global List)...")
		// Use a client from the first valid region for the initial global list call
		primaryRegion := targetRegions[0] // Simplified: assumes at least one valid region if we got here
		for _, r := range targetRegions {
			if strings.TrimSpace(r) != "" {
				primaryRegion = strings.TrimSpace(r)
				break
			}
		}

		s3Client, err := aws.NewAWSClient(primaryRegion)
		if err != nil {
			log.Error().Err(err).Str("region", primaryRegion).Msg("Failed to create AWS client for primary region S3 listing. Skipping S3.")
		} else {
			bucketInfos, err := s3Client.ListS3Buckets()
			if err != nil {
				log.Error().Err(err).Msg("Failed to list S3 buckets")
			} else {
				log.Info().Int("count", len(bucketInfos)).Msg("Fetched S3 Bucket list. Now fetching tags...")
				// Fetch tags for each bucket - GetS3BucketTags handles regional clients internally
				for _, bucketInfo := range bucketInfos {
					if bucketInfo.Name != nil {
						// Use the same s3Client, GetS3BucketTags will get/create the correct regional client
						tags, err := s3Client.GetS3BucketTags(bucketInfo.Name, bucketInfo.Region)
						if err != nil {
							log.Warn().Str("bucket", *bucketInfo.Name).Str("region", bucketInfo.Region).Msg("Using empty tags for bucket due to previous error")
							tags = make(map[string]string)
						}
						kind := "s3"
						name := *bucketInfo.Name
						region := bucketInfo.Region
						if *lowercaseFlag {
							kind = strings.ToLower(kind)
							name = strings.ToLower(name)
							region = strings.ToLower(region)
							tags = lowercaseTags(tags)
						}
						issues := policy.ValidateResource(kind, name, region, tags, tagPolicy, *lowercaseFlag)
						resInfo := ResourceInfo{ // Create struct but don't add AI recs yet
							Kind:   kind,
							Name:   name,
							Region: region,
							Tags:   tags,
							Issues: issues,
						}
						allResources = append(allResources, resInfo) // Add to the main slice
					}
				}
				log.Info().Msg("Finished fetching S3 tags.")
			}
		}
		// --- End S3 Buckets ---
		// --- End Fetch from AWS ---
	}

	// --- Prepare Good Examples for AI ---
	var goodExamples []ai.ResourceExample
	const maxExamples = 3         // Limit the number of examples passed to the AI
	if aiRecommendationsEnabled { // Check the derived boolean
		log.Debug().Msg("Searching for well-tagged resource examples...")
		count := 0
		for _, res := range allResources {
			if len(res.Issues) == 0 && count < maxExamples {
				goodExamples = append(goodExamples, ai.ResourceExample{
					Kind:   res.Kind,
					Name:   res.Name,
					Region: res.Region,
					Tags:   res.Tags,
				})
				count++
				log.Trace().Str("kind", res.Kind).Str("name", res.Name).Msg("Found good example resource")
			}
			if count >= maxExamples {
				break // Stop once we have enough examples
			}
		}
		log.Debug().Int("count", len(goodExamples)).Msg("Collected well-tagged examples for AI context")
	}
	// --- End Prepare Good Examples ---

	// --- Process AI Recommendations (if enabled) ---
	if aiRecommendationsEnabled { // Check the derived boolean
		log.Info().Dur("timeout", *aiTimeoutFlag).Msg("Fetching AI recommendations sequentially for resources with issues...")
		totalIssues := 0
		processedIssues := 0
		// Iterate through all collected resources
		for i := range allResources {
			res := &allResources[i] // Get pointer to modify the slice element
			if len(res.Issues) > 0 {
				log.Info().Str("kind", res.Kind).Str("name", res.Name).Int("issue_count", len(res.Issues)).Msg("Processing issues for resource")
				// Iterate through each issue for the current resource
				for j := range res.Issues {
					issue := &res.Issues[j] // Get pointer to modify the slice element
					totalIssues++

					// Log before sending request
					log.Debug().
						Str("kind", res.Kind).
						Str("name", res.Name).
						Str("issue_type", issue.IssueType).
						Str("tag_key", issue.TagKey).
						Msg("Querying AI for recommendation")

					recommendationStr, err := ai.GetAIRecommendation( // Renamed variable
						cfg.AIAPIURL,
						cfg.AIAPIToken,
						*aiModelFlag, // Use the model name directly
						res.Kind,
						res.Name,
						res.Region,
						res.Tags,
						*issue,
						goodExamples,
						*aiTimeoutFlag,
					)

					processedIssues++
					if err != nil {
						// Log error response
						log.Warn().Err(err).
							Str("kind", res.Kind).
							Str("name", res.Name).
							Str("issue_type", issue.IssueType).
							Str("tag_key", issue.TagKey).
							Msg("Failed to get AI recommendation")
						// Recommendation field remains nil/empty
					} else {
						// Log successful response
						log.Debug().
							Str("kind", res.Kind).
							Str("name", res.Name).
							Str("issue_type", issue.IssueType).
							Str("tag_key", issue.TagKey).
							Str("recommendation", recommendationStr). // Log the received recommendation string
							Str("model", *aiModelFlag).
							Msg("Received AI recommendation")
						// Assign recommendation as a map with model name as key
						issue.Recommendation = map[string]string{
							*aiModelFlag: recommendationStr, // Use the model name directly
						}
					}
				} // End issue loop
			}
		} // End resource loop
		log.Info().Int("processed_issues", processedIssues).Int("total_issues", totalIssues).Msg("Finished fetching AI recommendations.")
	}
	// --- End AI Processing ---

	log.Info().Msg("Marshalling results to JSON...")
	// Marshal the collected resources (now potentially with AI recommendations) into JSON
	jsonData, err := json.MarshalIndent(allResources, "", "  ")
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to marshal resources to JSON")
	}

	// Print the JSON output to stdout
	fmt.Println(string(jsonData))

	// Log the total count of processed resources to stderr
	log.Info().Int("totalResources", len(allResources)).Msg("Processing complete.")
}

// runApplyCommand handles the logic for applying recommended tags.
func runApplyCommand(cfg *config.Config, inputFlag *string, kindFlag *string) { // Add kindFlag parameter
	log.Info().Str("file", *inputFlag).Msg("Running apply command...")

	// Process kind flag
	allowedKinds := make(map[string]bool)
	filterByKind := false
	if *kindFlag != "" {
		filterByKind = true
		kinds := strings.Split(*kindFlag, ",")
		for _, k := range kinds {
			trimmedKind := strings.TrimSpace(k)
			if trimmedKind != "" {
				allowedKinds[trimmedKind] = true
				log.Info().Str("kind", trimmedKind).Msg("Filtering for resource kind")
			}
		}
		if len(allowedKinds) == 0 {
			log.Warn().Str("flagValue", *kindFlag).Msg("Kind filter flag was provided but contained no valid kinds after splitting and trimming.")
			filterByKind = false // Treat as no filter if empty after processing
		}
	}

	// Read the input JSON file
	jsonData, err := os.ReadFile(*inputFlag)
	if err != nil {
		log.Fatal().Err(err).Str("file", *inputFlag).Msg("Failed to read input JSON file")
	}

	// Unmarshal the JSON data
	var allResources []ResourceInfo
	err = json.Unmarshal(jsonData, &allResources)
	if err != nil {
		log.Fatal().Err(err).Str("file", *inputFlag).Msg("Failed to unmarshal JSON data from input file")
	}
	log.Info().Int("count", len(allResources)).Msg("Successfully loaded resources for application")

	reader := bufio.NewReader(os.Stdin) // For user input

	appliedCount := 0
	skippedCount := 0
	errorCount := 0

	// Iterate through resources and issues
	for _, resource := range allResources {
		// --- Filter by Kind ---
		if filterByKind {
			if _, ok := allowedKinds[resource.Kind]; !ok {
				log.Trace().Str("kind", resource.Kind).Str("name", resource.Name).Msg("Skipping resource due to kind filter")
				continue // Skip this resource if kind doesn't match the allowed list
			}
		}
		// --- End Filter by Kind ---

		if len(resource.Issues) == 0 {
			continue
		}

		for _, issue := range resource.Issues {
			if issue.Recommendation == nil || len(issue.Recommendation) == 0 {
				continue
			}

			// --- Display Common Resource Info ---
			fmt.Printf("Resource: %s %s (%s)\n", resource.Kind, resource.Name, resource.Region)
			// Display "Name" tag if it exists
			if nameTag, ok := resource.Tags["Name"]; ok {
				fmt.Printf("  Name Tag: '%s'\n", nameTag)
			} else if nameTag, ok := resource.Tags["name"]; ok { // Check lowercase too
				fmt.Printf("  Name Tag: '%s'\n", nameTag)
			}
			// Display all existing tags
			fmt.Println("  Existing Tags:")
			if len(resource.Tags) > 0 {
				for k, v := range resource.Tags {
					fmt.Printf("    %s: %s\n", k, v)
				}
			} else {
				fmt.Println("    (No tags found)")
			}
			fmt.Printf("  Issue: %s (%s)\n", issue.IssueType, issue.TagKey)

			// --- Handle Single vs Multiple Recommendations ---
			if len(issue.Recommendation) == 1 {
				// --- Single Recommendation Logic ---
				var model, recString string
				for m, r := range issue.Recommendation {
					model, recString = m, r
					break
				}

				log.Debug().Str("kind", resource.Kind).Str("name", resource.Name).Str("model", model).Str("rec", recString).Msg("Processing single recommendation")

				action, key, value, err := recommendation.ParseRecommendation(recString)
				if err != nil {
					log.Error().Err(err).Str("recommendation", recString).Msg("Failed to parse recommendation, skipping")
					errorCount++
					continue
				}

				// Display current state of the target tag
				currentValue, exists := resource.Tags[key]
				if exists {
					fmt.Printf("  Current value for tag '%s': '%s'\n", key, currentValue)
				} else {
					fmt.Printf("  Tag '%s' is currently missing.\n", key)
				}
				// Display recommendation
				fmt.Printf("  Recommendation (%s): %s tag '%s' with value '%s'\n", model, action, key, value)
				fmt.Print("Apply this change? (y/N/e[dit]): ") // Updated prompt

				inputText, _ := reader.ReadString('\n')
				inputText = strings.TrimSpace(strings.ToLower(inputText))

				applyChange := false
				editedValue := value // Start with the recommended value

				switch inputText {
				case "y":
					applyChange = true
					log.Info().Str("action", action).Str("key", key).Str("value", value).Str("kind", resource.Kind).Str("name", resource.Name).Msg("User approved applying change")
				case "e":
					fmt.Printf("Enter new value for tag '%s': ", key)
					newValueText, _ := reader.ReadString('\n')
					editedValue = strings.TrimSpace(newValueText)
					applyChange = true
					log.Info().Str("action", action).Str("key", key).Str("originalValue", value).Str("newValue", editedValue).Str("kind", resource.Kind).Str("name", resource.Name).Msg("User approved applying edited change")
				default: // Includes "n" and anything else
					log.Warn().Str("action", action).Str("key", key).Str("value", value).Str("kind", resource.Kind).Str("name", resource.Name).Msg("User skipped applying change")
					skippedCount++
				}

				if applyChange {
					err := applyTagChange(cfg, resource, action, key, editedValue) // Use editedValue
					if err != nil {
						log.Error().Err(err).Msg("Failed to apply tag change")
						errorCount++
					} else {
						log.Info().Msg("Successfully applied tag change")
						appliedCount++
					}
				}
				fmt.Println("---") // Separator

			} else {
				// --- Multiple Recommendations Logic ---
				log.Debug().Str("kind", resource.Kind).Str("name", resource.Name).Int("count", len(issue.Recommendation)).Msg("Processing multiple recommendations")

				// Determine the target key and display current state
				var targetKey string
				var firstRecParsed bool
				for _, rec := range issue.Recommendation {
					_, k, _, err := recommendation.ParseRecommendation(rec)
					if err == nil {
						targetKey = k
						firstRecParsed = true
						break
					}
				}
				if firstRecParsed {
					currentValue, exists := resource.Tags[targetKey]
					if exists {
						fmt.Printf("  Current value for tag '%s': '%s'\n", targetKey, currentValue)
					} else {
						fmt.Printf("  Tag '%s' is currently missing.\n", targetKey)
					}
				} else {
					log.Warn().Str("kind", resource.Kind).Str("name", resource.Name).Str("issue", issue.IssueType).Msg("Could not parse any recommendation to determine target key for showing current value.")
				}

				fmt.Println("  Multiple recommendations found:")

				// Store recommendations in a slice and display choices
				type recChoice struct {
					model string
					rec   string
				}
				choices := make([]recChoice, 0, len(issue.Recommendation))
				i := 1
				for model, rec := range issue.Recommendation {
					choices = append(choices, recChoice{model: model, rec: rec})
					fmt.Printf("    %d. [%s]: %s\n", i, model, rec)
					i++
				}

				fmt.Printf("Enter the number of the recommendation to apply (1-%d), 'e' to edit the chosen value, or anything else to skip: ", len(choices)) // Updated prompt
				inputText, _ := reader.ReadString('\n')
				inputText = strings.TrimSpace(strings.ToLower(inputText))

				applyChange := false
				editChoice := false
				var selectedChoice recChoice
				var action, key, value, editedValue string // Declare vars needed later

				choiceIndex, err := strconv.Atoi(inputText)
				if err == nil && choiceIndex >= 1 && choiceIndex <= len(choices) {
					// Valid numeric choice - potentially apply or edit
					selectedChoice = choices[choiceIndex-1]
					log.Info().Str("kind", resource.Kind).Str("name", resource.Name).Int("choice", choiceIndex).Str("model", selectedChoice.model).Str("rec", selectedChoice.rec).Msg("User selected recommendation")
					// Ask if they want to edit this choice
					fmt.Printf("Apply recommendation %d ([%s]: %s)? (y/N/e[dit]): ", choiceIndex, selectedChoice.model, selectedChoice.rec)
					confirmText, _ := reader.ReadString('\n')
					confirmText = strings.TrimSpace(strings.ToLower(confirmText))

					if confirmText == "y" {
						applyChange = true
					} else if confirmText == "e" {
						applyChange = true
						editChoice = true
					} else {
						// Skipped after choosing
						log.Warn().Str("input", confirmText).Str("kind", resource.Kind).Str("name", resource.Name).Msg("User skipped applying chosen change")
						skippedCount++
					}

				} else if inputText == "e" {
					// User wants to edit, but needs to choose which one first
					fmt.Printf("Which recommendation number (1-%d) do you want to edit and apply?: ", len(choices))
					editChoiceText, _ := reader.ReadString('\n')
					editChoiceText = strings.TrimSpace(editChoiceText)
					editIndex, editErr := strconv.Atoi(editChoiceText)
					if editErr == nil && editIndex >= 1 && editIndex <= len(choices) {
						selectedChoice = choices[editIndex-1]
						log.Info().Str("kind", resource.Kind).Str("name", resource.Name).Int("choice", editIndex).Str("model", selectedChoice.model).Str("rec", selectedChoice.rec).Msg("User selected recommendation to edit")
						applyChange = true
						editChoice = true
					} else {
						log.Warn().Str("input", editChoiceText).Str("kind", resource.Kind).Str("name", resource.Name).Msg("Invalid choice for editing, skipping")
						skippedCount++
					}
				} else {
					// Invalid initial choice or non-numeric input -> skip
					log.Warn().Str("input", inputText).Str("kind", resource.Kind).Str("name", resource.Name).Msg("User skipped applying change (invalid initial choice or non-numeric/non-'e' input)")
					skippedCount++
				}

				// If a valid choice was made and user wants to apply (potentially after editing)
				if applyChange {
					var parseErr error
					action, key, value, parseErr = recommendation.ParseRecommendation(selectedChoice.rec)
					if parseErr != nil {
						log.Error().Err(parseErr).Str("recommendation", selectedChoice.rec).Msg("Failed to parse selected recommendation, skipping")
						errorCount++
					} else {
						editedValue = value // Default to recommended value
						if editChoice {
							fmt.Printf("Original suggested value for tag '%s': '%s'\n", key, value)
							fmt.Printf("Enter new value for tag '%s': ", key)
							newValueText, _ := reader.ReadString('\n')
							editedValue = strings.TrimSpace(newValueText)
							log.Info().Str("action", action).Str("key", key).Str("originalValue", value).Str("newValue", editedValue).Str("kind", resource.Kind).Str("name", resource.Name).Msg("Applying edited change")
						} else {
							log.Info().Str("action", action).Str("key", key).Str("value", value).Str("kind", resource.Kind).Str("name", resource.Name).Msg("Applying selected change")
						}

						applyErr := applyTagChange(cfg, resource, action, key, editedValue) // Use editedValue
						if applyErr != nil {
							log.Error().Err(applyErr).Msg("Failed to apply tag change")
							errorCount++
						} else {
							log.Info().Msg("Successfully applied tag change")
							appliedCount++
						}
					}
				}
				fmt.Println("---") // Separator
			}
		}
	}

	log.Info().
		Int("applied", appliedCount).
		Int("skipped", skippedCount).
		Int("errors", errorCount).
		Msg("Apply command finished.")
}

// applyTagChange dispatches the tag application to the correct AWS service function.
func applyTagChange(cfg *config.Config, resource ResourceInfo, action, key, value string) error {
	log.Debug().Str("kind", resource.Kind).Str("name", resource.Name).Str("region", resource.Region).Msg("Attempting to apply tag")

	// Create AWS client for the specific region of the resource
	// Note: We might create redundant clients if multiple resources are in the same region,
	// but client creation is relatively cheap and simplifies logic here.
	// S3 client creation is handled within the ApplyTagsToS3Bucket function.
	client, err := aws.NewAWSClient(resource.Region)
	if err != nil {
		return fmt.Errorf("failed to create AWS client for region %s: %w", resource.Region, err)
	}

	tagsToApply := map[string]string{key: value}

	switch resource.Kind {
	case "ec2", "ebs", "vpc", "natgateway", "internetgateway", "eip":
		// These resources use EC2:CreateTags
		// The 'name' field holds the Resource ID for these types
		return client.ApplyTagsToEC2Resource(resource.Name, tagsToApply)
	case "elbv2":
		// The 'name' field holds the ARN for ELBv2
		return client.ApplyTagsToELBv2Resource(resource.Name, tagsToApply)
	case "ecscluster":
		// The 'name' field holds the ARN for ECS Cluster
		return client.ApplyTagsToECSResource(resource.Name, tagsToApply)
	case "s3":
		// The 'name' field holds the bucket name
		// ApplyTagsToS3Bucket needs the bucket name and region
		return client.ApplyTagsToS3Bucket(resource.Name, resource.Region, tagsToApply)
	default:
		return fmt.Errorf("unsupported resource kind for applying tags: %s", resource.Kind)
	}
}
