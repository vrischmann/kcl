// Package misc contains miscellaneous, unspecific commands.
package misc

import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"sort"
	"strconv"
	"time"

	"github.com/spf13/cobra"

	"github.com/twmb/kafka-go/pkg/kerr"
	"github.com/twmb/kafka-go/pkg/kgo"
	"github.com/twmb/kafka-go/pkg/kmsg"
	"github.com/twmb/kafka-go/pkg/kversion"

	"github.com/twmb/kcl/client"
	"github.com/twmb/kcl/flagutil"
	"github.com/twmb/kcl/out"
)

func apiVersionsRequest() *kmsg.ApiVersionsRequest {
	return &kmsg.ApiVersionsRequest{
		ClientSoftwareName:    "kcl",
		ClientSoftwareVersion: "v0.0.0",
	}
}

func Command(cl *client.Client) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "misc",
		Short: "Miscellaneous utilities",
	}

	cmd.AddCommand(errcodeCommand())
	cmd.AddCommand(genAutocompleteCommand())
	cmd.AddCommand(apiVersionsCommand(cl))
	cmd.AddCommand(probeVersionCommand(cl))
	cmd.AddCommand(rawCommand(cl))
	cmd.AddCommand(listOffsetsCommand(cl))

	return cmd
}

func errcodeCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "errcode CODE",
		Short: "Print the name and message for an error code",
		Args:  cobra.ExactArgs(1),
		Run: func(_ *cobra.Command, args []string) {
			code, err := strconv.Atoi(args[0])
			if err != nil {
				out.Die("unable to parse error code: %v", err)
			}
			if code == 0 {
				fmt.Println("NONE")
				return
			}
			kerr := kerr.ErrorForCode(int16(code)).(*kerr.Error)
			fmt.Printf("%s\n%s\n", kerr.Message, kerr.Description)
		},
	}
}

func genAutocompleteCommand() *cobra.Command {
	var kind string

	cmd := &cobra.Command{
		Use:   "gen-autocomplete",
		Short: "Generates bash completion scripts",
		Long: `To load completion run

. <(kcl misc gen-autocomplete -kbash)

To configure your shell to load completions for each session add to your bashrc
(or equivalent, for your shell depending on support):

# ~/.bashrc or ~/.profile
if [ -f /etc/bash_completion ] && ! shopt -oq posix; then
    . /etc/bash_completion
    . <(kcl misc gen-autocomplete -kbash)
fi

This command supports completion for bash, zsh, and powershell.
`,
		Args: cobra.ExactArgs(0),
		Run: func(cmd *cobra.Command, _ []string) {
			switch kind {
			case "bash":
				cmd.Root().GenBashCompletion(os.Stdout)
			case "zsh":
				cmd.Root().GenZshCompletion(os.Stdout)
			case "powershell":
				cmd.Root().GenPowerShellCompletion(os.Stdout)
			default:
				out.Die("unrecognized autocomplete kind %q", kind)
			}
		},
	}

	cmd.Flags().StringVarP(&kind, "kind", "k", "bash", "autocomplete kind (bash, zsh, powershell)")

	return cmd
}

func apiVersionsCommand(cl *client.Client) *cobra.Command {
	return &cobra.Command{
		Use:   "api-versions",
		Short: "Print broker API versions for each Kafka request type (Kafka 0.10.0+).",
		Args:  cobra.ExactArgs(0),
		Run: func(_ *cobra.Command, _ []string) {
			kresp, err := cl.Client().Request(context.Background(), apiVersionsRequest())
			out.MaybeDie(err, "unable to request API versions: %v", err)

			if cl.AsJSON() {
				out.ExitJSON(kresp)
			}

			tw := out.BeginTabWrite()
			defer tw.Flush()

			resp := kresp.(*kmsg.ApiVersionsResponse)

			fmt.Fprintf(tw, "NAME\tMAX\n")
			for _, k := range resp.ApiKeys {
				kind := kmsg.NameForKey(k.ApiKey)
				if kind == "" {
					kind = "Unknown"
				}
				fmt.Fprintf(tw, "%s\t%d\n", kind, k.MaxVersion)
			}
		},
	}
}

func probeVersionCommand(cl *client.Client) *cobra.Command {
	return &cobra.Command{
		Use:   "probe-version",
		Short: "Probe and print the version of Kafka running (incompatable with --as-version)",
		Args:  cobra.ExactArgs(0),
		Run: func(_ *cobra.Command, _ []string) {
			probeVersion(cl)
		},
	}
}

// probeVersion prints what version of Kafka the client is interacting with.
func probeVersion(cl *client.Client) {
	// If we request against a Kafka older than ApiVersions,
	// Kafka will close the connection. ErrConnDead is
	// retried automatically, so we must stop that.
	cl.AddOpt(kgo.RequestRetries(0))
	kresp, err := cl.Client().Request(context.Background(), apiVersionsRequest())
	if err != nil { // pre 0.10.0 had no api versions
		cl.RemakeWithOpts(kgo.MaxVersions(kversion.V0_9_0()))
		// 0.9.0 has list groups
		if _, err = cl.Client().SeedBrokers()[0].Request(context.Background(), new(kmsg.ListGroupsRequest)); err == nil {
			fmt.Println("Kafka 0.9.0")
			return
		}
		cl.RemakeWithOpts(kgo.MaxVersions(kversion.V0_8_2()))
		// 0.8.2 has find coordinator
		if _, err = cl.Client().SeedBrokers()[0].Request(context.Background(), new(kmsg.FindCoordinatorRequest)); err == nil {
			fmt.Println("Kafka 0.8.2")
			return
		}
		cl.RemakeWithOpts(kgo.MaxVersions(kversion.V0_8_1()))
		// 0.8.1 has offset fetch
		if _, err = cl.Client().SeedBrokers()[0].Request(context.Background(), new(kmsg.OffsetFetchRequest)); err == nil {
			fmt.Println("Kafka 0.8.1")
			return
		}
		fmt.Println("Kafka 0.8.0")
		return
	}

	resp := kresp.(*kmsg.ApiVersionsResponse)
	got := make(kversion.Versions, len(resp.ApiKeys))
	for i, key := range resp.ApiKeys {
		got[i] = key.MaxVersion
	}

	eq := func(test kversion.Versions) bool {
		if len(test) != len(got) {
			return false
		}
		for i, v := range test {
			if got[i] != v {
				return false
			}
		}
		return true
	}

	switch {
	case eq(kversion.V0_10_0()):
		fmt.Println("Kafka 0.10.0")
	case eq(kversion.V0_10_1()):
		fmt.Println("Kafka 0.10.1")
	case eq(kversion.V0_10_2()):
		fmt.Println("Kafka 0.10.2")
	case eq(kversion.V0_11_0()):
		fmt.Println("Kafka 0.11.0")
	case eq(kversion.V1_0_0()):
		fmt.Println("Kafka 1.0.0")
	case eq(kversion.V1_1_0()):
		fmt.Println("Kafka 1.1.0")
	case eq(kversion.V2_0_0()):
		fmt.Println("Kafka 2.0.0")
	case eq(kversion.V2_1_0()):
		fmt.Println("Kafka 2.1.0")
	case eq(kversion.V2_2_0()):
		fmt.Println("Kafka 2.2.0")
	case eq(kversion.V2_3_0()):
		fmt.Println("Kafka 2.3.0")
	case eq(kversion.V2_4_0()):
		fmt.Println("Kafka 2.4.0")
	case eq(kversion.V2_5_0()):
		fmt.Println("Kafka 2.5.0")
	default:
		fmt.Println("Unknown version: either tip or between releases")
	}
}

func rawCommand(cl *client.Client) *cobra.Command {
	var key int16
	cmd := &cobra.Command{
		Use:   "raw-req",
		Short: "Issue an arbitrary request parsed from JSON read from STDIN.",
		Args:  cobra.ExactArgs(0),
		Run: func(_ *cobra.Command, _ []string) {
			req := kmsg.RequestForKey(key)
			if req == nil {
				out.Die("request key %d unknown", key)
			}
			raw, err := ioutil.ReadAll(os.Stdin)
			out.MaybeDie(err, "unable to read stdin: %v", err)
			err = json.Unmarshal(raw, req)
			out.MaybeDie(err, "unable to unmarshal stdin: %v", err)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			kresp, err := cl.Client().Request(ctx, req)
			out.MaybeDie(err, "response error: %v", err)
			out.ExitJSON(kresp)
		},
	}
	cmd.Flags().Int16VarP(&key, "key", "k", -1, "request key")
	return cmd
}

func listOffsetsCommand(cl *client.Client) *cobra.Command {
	var withEpochs bool
	var readCommitted bool

	cmd := &cobra.Command{
		Use:   "list-offsets",
		Short: "List start and end offsets for partitions.",
		Long: `List start and end offsets for topics or partitions (Kafka 0.8.0+).

The input format is topic:#,#,# or just topic. If a topic is given without
partitions, a metadata request is issued to figure out all partitions for the
topic and the output will include the start and end offsets for all partitions.

Multiple topics can be listed, and multiple partitions per topic can be listed.

If --with-epochs is true, the start and end offsets will have /### following
the offset number, where ### corresponds to the broker epoch at at that given
offset.
`,
		Example: "list-offsets foo:1,2,3 bar:0",
		Args:    cobra.MinimumNArgs(1),
		Run: func(_ *cobra.Command, topicParts []string) {
			tps, err := flagutil.ParseTopicPartitions(topicParts)
			out.MaybeDie(err, "unable to parse topic partitions: %v", err)

			for topic, partitions := range tps {
				var metaTopics []kmsg.MetadataRequestTopic
				if len(partitions) == 0 {
					metaTopics = append(metaTopics, kmsg.MetadataRequestTopic{Topic: topic})
				}
				if len(metaTopics) > 0 {
					kresp, err := cl.Client().Request(context.Background(), &kmsg.MetadataRequest{Topics: metaTopics})
					out.MaybeDie(err, "unable to get metadata: %v", err)
					resp := kresp.(*kmsg.MetadataResponse)
					for _, topic := range resp.Topics {
						for _, partition := range topic.Partitions {
							tps[topic.Topic] = append(tps[topic.Topic], partition.Partition)
						}
					}
				}
			}

			req := &kmsg.ListOffsetsRequest{
				ReplicaID:      -1,
				IsolationLevel: 0,
			}
			if readCommitted {
				req.IsolationLevel = 1
			}
			for topic, partitions := range tps {
				topicReq := kmsg.ListOffsetsRequestTopic{
					Topic: topic,
				}
				for _, partition := range partitions {
					topicReq.Partitions = append(topicReq.Partitions, kmsg.ListOffsetsRequestTopicPartition{
						Partition:          partition,
						CurrentLeaderEpoch: -1,
						Timestamp:          -2, // earliest
						MaxNumOffsets:      1,  // just in case <= 0.10.0
					})
				}
				req.Topics = append(req.Topics, topicReq)
			}

			kresp, err := cl.Client().Request(context.Background(), req)
			out.MaybeDie(err, "unable to list start offsets: %v", err)

			startResp := kresp.(*kmsg.ListOffsetsResponse)

			for topic := range req.Topics {
				for partition := range req.Topics[topic].Partitions {
					req.Topics[topic].Partitions[partition].Timestamp = -1 // latest
				}
			}

			kresp, err = cl.Client().Request(context.Background(), req)
			out.MaybeDie(err, "unable to list end offsets: %v", err)

			endResp := kresp.(*kmsg.ListOffsetsResponse)

			type startEnd struct {
				err              error
				startOffset      int64
				startLeaderEpoch int32
				endOffset        int64
				endLeaderEpoch   int32
			}

			startEnds := make(map[string]map[int32]startEnd)

			for _, topic := range startResp.Topics {
				topicStartEnds := make(map[int32]startEnd)
				startEnds[topic.Topic] = topicStartEnds
				for _, partition := range topic.Partitions {
					if startResp.Version == 0 && len(partition.OldStyleOffsets) > 0 {
						partition.Offset = partition.OldStyleOffsets[0]
					}
					topicStartEnds[partition.Partition] = startEnd{
						err:              kerr.ErrorForCode(partition.ErrorCode),
						startOffset:      partition.Offset,
						startLeaderEpoch: partition.LeaderEpoch,
					}
				}
			}

			for _, topic := range endResp.Topics {
				topicStartEnds := startEnds[topic.Topic]
				var startErr bool
				if topicStartEnds == nil {
					topicStartEnds = make(map[int32]startEnd)
					startEnds[topic.Topic] = topicStartEnds
					startErr = true
				}
				for _, partition := range topic.Partitions {
					partStartEnd, ok := topicStartEnds[partition.Partition]
					if !ok {
						startErr = true
					}
					if endResp.Version == 0 && len(partition.OldStyleOffsets) > 0 {
						partition.Offset = partition.OldStyleOffsets[0]
					}
					partStartEnd.endOffset = partition.Offset
					partStartEnd.endLeaderEpoch = partition.LeaderEpoch

					if err := kerr.ErrorForCode(partition.ErrorCode); err != nil {
						partStartEnd.err = err
					} else if startErr {
						partStartEnd.err = kerr.UnknownServerError
					}

					topicStartEnds[partition.Partition] = partStartEnd
				}
			}

			type partStartEnd struct {
				part int32
				startEnd
			}
			type sortedTopic struct {
				topic string
				parts []partStartEnd
			}
			var sorted []sortedTopic
			for topic, partitions := range startEnds {
				st := sortedTopic{topic: topic}
				for part, startEnd := range partitions {
					st.parts = append(st.parts, partStartEnd{part: part, startEnd: startEnd})
				}
				sort.Slice(st.parts, func(i, j int) bool { return st.parts[i].part < st.parts[j].part })
				sorted = append(sorted, st)
			}
			sort.Slice(sorted, func(i, j int) bool { return sorted[i].topic < sorted[j].topic })

			tw := out.BeginTabWrite()
			defer tw.Flush()

			fmt.Fprintf(tw, "TOPIC\tPARTITION\tSTART\tEND\tERROR\n")

			for _, topic := range sorted {
				for _, part := range topic.parts {
					if part.err != nil {
						fmt.Fprintf(tw, "%s\t%d\t\t\t%v\n", topic.topic, part.part, part.err)
						continue
					}
					if withEpochs {
						fmt.Fprintf(tw, "%s\t%d\t%d/%d\t%d/%d\t\n",
							topic.topic,
							part.part,
							part.startOffset,
							part.startLeaderEpoch,
							part.endOffset,
							part.endLeaderEpoch,
						)
					} else {
						fmt.Fprintf(tw, "%s\t%d\t%d\t%d\t\n",
							topic.topic,
							part.part,
							part.startOffset,
							part.endOffset,
						)
					}
				}
			}
		},
	}

	cmd.Flags().BoolVar(&readCommitted, "committed", false, "whether to list only committed offsets as opposed to latest (Kafka 0.11.0+)")
	cmd.Flags().BoolVar(&withEpochs, "with-epochs", false, "whether to include the epoch for the start and end offsets (Kafka 2.1.0+)")

	return cmd
}
