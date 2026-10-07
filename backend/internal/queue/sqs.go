package queue

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"
)

// SQSAPI is the subset of *sqs.Client used here.
type SQSAPI interface {
	SendMessageBatch(context.Context, *sqs.SendMessageBatchInput, ...func(*sqs.Options)) (*sqs.SendMessageBatchOutput, error)
	ChangeMessageVisibility(context.Context, *sqs.ChangeMessageVisibilityInput, ...func(*sqs.Options)) (*sqs.ChangeMessageVisibilityOutput, error)
}

// SQS sends to the notion-sync FIFO queue.
type SQS struct {
	API      SQSAPI
	QueueURL string
}

// Send uses SendMessageBatch, 10 entries per call. Any failed entry is an error.
func (q *SQS) Send(ctx context.Context, msgs ...Outgoing) error {
	for start := 0; start < len(msgs); start += 10 {
		chunk := msgs[start:min(start+10, len(msgs))]
		entries := make([]types.SendMessageBatchRequestEntry, len(chunk))
		for i, o := range chunk {
			body, err := json.Marshal(o.Message)
			if err != nil {
				return err
			}
			entries[i] = types.SendMessageBatchRequestEntry{
				Id:                     aws.String(strconv.Itoa(i)),
				MessageBody:            aws.String(string(body)),
				MessageGroupId:         aws.String(o.Message.GroupID()),
				MessageDeduplicationId: aws.String(o.DedupID),
			}
		}
		out, err := q.API.SendMessageBatch(ctx, &sqs.SendMessageBatchInput{QueueUrl: aws.String(q.QueueURL), Entries: entries})
		if err != nil {
			return fmt.Errorf("queue: send: %w", err)
		}
		if len(out.Failed) > 0 {
			return fmt.Errorf("queue: %d of %d messages failed (%s)", len(out.Failed), len(entries), aws.ToString(out.Failed[0].Code))
		}
	}
	return nil
}

// ChangeVisibility hides a message for d, rounded up to whole seconds (Notion 429, spec §2.15).
func (q *SQS) ChangeVisibility(ctx context.Context, receiptHandle string, d time.Duration) error {
	secs := int32(min(math.Ceil(d.Seconds()), 43200))
	_, err := q.API.ChangeMessageVisibility(ctx, &sqs.ChangeMessageVisibilityInput{
		QueueUrl: aws.String(q.QueueURL), ReceiptHandle: aws.String(receiptHandle), VisibilityTimeout: secs,
	})
	return err
}
