package store

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

// ArticleMeta is the descriptive part of an article, written once (if_not_exists).
type ArticleMeta struct {
	CanonicalURL string
	Source       string
	Title        string
	Author       string
	SiteName     string
	WordCount    int
	PublishedAt  string
}

// SegmentWrite is one validated segment ready for the push transaction.
type SegmentWrite struct {
	InstallationID string
	DeviceID       string
	SegmentID      string
	SessionID      string
	Seq            int
	URLHash        string
	Date           string // YYYY-MM-DD of StartedAt in the request tz
	StartedAt      time.Time
	EndedAt        time.Time
	ActiveSeconds  int
	MaxProgress    float64
	Article        ArticleMeta
}

type PushOutcome int

const (
	PushAccepted PushOutcome = iota + 1
	PushDuplicate
	PushRetry
)

const (
	segmentTTL       = 90 * 24 * time.Hour
	conflictAttempts = 3
)

// PushSegment applies one segment exactly once (C2): the SEG put plus the ART and
// DAY increments commit together or not at all. The monotonic max_progress and
// last_read_at updates run after the commit (see raiseArticleMax); if they cannot be
// applied the outcome is PushRetry, so the client keeps the segment and resends it.
// The resend hits the duplicate path, which re-applies them (they are idempotent)
// without counting the segment twice.
func (s *Store) PushSegment(ctx context.Context, w SegmentWrite) (PushOutcome, error) {
	for attempt := 1; ; attempt++ {
		// A fresh input per attempt: the SDK stamps an idempotency token on the struct it
		// is given, and a retry must not replay the token of a cancelled attempt.
		_, err := s.db.TransactWriteItems(ctx, s.pushTx(w))
		if err == nil {
			return s.finishPush(ctx, w, PushAccepted), nil
		}
		switch classifyTx(err) {
		case txDuplicate:
			return s.finishPush(ctx, w, PushDuplicate), nil
		case txThrottled:
			return PushRetry, nil
		case txConflict:
			// Another push is updating the same ART/DAY item right now (scenario 4).
			if attempt >= conflictAttempts {
				return PushRetry, nil
			}
			s.sleep(time.Duration(attempt*50) * time.Millisecond)
		default:
			return 0, err
		}
	}
}

// finishPush runs the post-commit step for a segment that is committed (accepted or
// already stored) and downgrades the outcome to PushRetry when it cannot complete.
func (s *Store) finishPush(ctx context.Context, w SegmentWrite, outcome PushOutcome) PushOutcome {
	if err := s.raiseArticleMax(ctx, w); err != nil {
		slog.WarnContext(ctx, "store: post-commit update failed; asking client to retry",
			"segment_id", w.SegmentID, "err", err)
		return PushRetry
	}
	return outcome
}

func (s *Store) pushTx(w SegmentWrite) *dynamodb.TransactWriteItemsInput {
	pk := InstPK(w.InstallationID)
	started := Timestamp(w.StartedAt)
	first := int64(0)
	if w.Seq == 0 {
		first = 1
	}
	seg := map[string]types.AttributeValue{
		"pk": str(pk), "sk": str(SegmentSK(w.SegmentID)),
		"session_id": str(w.SessionID), "seq": numI(int64(w.Seq)), "url_hash": str(w.URLHash),
		"device_id": str(w.DeviceID), "started_at": str(started), "ended_at": str(Timestamp(w.EndedAt)),
		"active_seconds": numI(int64(w.ActiveSeconds)), "max_progress": numF(w.MaxProgress),
		"ttl": numI(w.StartedAt.Add(segmentTTL).Unix()),
	}
	a := w.Article
	return &dynamodb.TransactWriteItemsInput{TransactItems: []types.TransactWriteItem{
		{Put: &types.Put{TableName: &s.table, Item: seg, ConditionExpression: aws.String("attribute_not_exists(pk)")}},
		{Update: &types.Update{
			TableName: &s.table,
			Key:       key(pk, ArticleSK(w.URLHash)),
			UpdateExpression: aws.String("SET #u = if_not_exists(#u, :u), #src = if_not_exists(#src, :src), " +
				"#t = if_not_exists(#t, :t), #a = if_not_exists(#a, :a), #sn = if_not_exists(#sn, :sn), " +
				"#wc = if_not_exists(#wc, :wc), #pa = if_not_exists(#pa, :pa), #fr = if_not_exists(#fr, :st) " +
				"ADD #ts :as, #ss :first, #v :one"),
			ExpressionAttributeNames: map[string]string{
				"#u": "canonical_url", "#src": "source", "#t": "title", "#a": "author", "#sn": "site_name",
				"#wc": "word_count", "#pa": "published_at", "#fr": "first_read_at",
				"#ts": "total_seconds", "#ss": "sessions", "#v": "version",
			},
			ExpressionAttributeValues: map[string]types.AttributeValue{
				":u": str(a.CanonicalURL), ":src": str(a.Source), ":t": str(a.Title), ":a": str(a.Author),
				":sn": str(a.SiteName), ":wc": numI(int64(a.WordCount)), ":pa": str(a.PublishedAt), ":st": str(started),
				":as": numI(int64(w.ActiveSeconds)), ":first": numI(first), ":one": numI(1),
			},
		}},
		{Update: &types.Update{
			TableName:                &s.table,
			Key:                      key(pk, DaySK(w.Date)),
			UpdateExpression:         aws.String("SET #d = :date ADD #s :as, #v :one, #h :h"),
			ExpressionAttributeNames: map[string]string{"#d": "date", "#s": "seconds", "#v": "version", "#h": "article_hashes"},
			ExpressionAttributeValues: map[string]types.AttributeValue{
				":date": str(w.Date), ":as": numI(int64(w.ActiveSeconds)), ":one": numI(1),
				":h": &types.AttributeValueMemberSS{Value: []string{w.URLHash}},
			},
		}},
	}}
}

// raiseArticleMax applies the post-commit monotonic updates (spec §3.2). They sit
// outside the transaction because DynamoDB cannot express "set if greater" next to
// ADD on the same item. Both are idempotent, so PushSegment re-runs them on a
// duplicate. It returns the first error; only a failed condition (the stored value
// is already as large) is ignored.
func (s *Store) raiseArticleMax(ctx context.Context, w SegmentWrite) error {
	k := key(InstPK(w.InstallationID), ArticleSK(w.URLHash))
	if w.MaxProgress > 0 {
		if err := s.setIfGreater(ctx, k, "max_progress", numF(w.MaxProgress)); err != nil {
			return err
		}
	}
	return s.setIfGreater(ctx, k, "last_read_at", str(Timestamp(w.EndedAt)))
}

// setIfGreater raises attr to v unless it is already >= v. A plain UpdateItem on an
// item with a transaction in flight fails with TransactionConflictException, which
// the SDK retryer does not retry, so conflicts and throttling are retried here with
// the same back-off as the push transaction.
func (s *Store) setIfGreater(ctx context.Context, k map[string]types.AttributeValue, attr string, v types.AttributeValue) error {
	in := &dynamodb.UpdateItemInput{
		TableName:                 &s.table,
		Key:                       k,
		UpdateExpression:          aws.String("SET #f = :v"),
		ConditionExpression:       aws.String("attribute_not_exists(#f) OR #f < :v"),
		ExpressionAttributeNames:  map[string]string{"#f": attr},
		ExpressionAttributeValues: map[string]types.AttributeValue{":v": v},
	}
	for attempt := 1; ; attempt++ {
		_, err := s.db.UpdateItem(ctx, in)
		switch {
		case err == nil, isConditionFailed(err):
			return nil
		case attempt < conflictAttempts && (isTxConflict(err) || IsThrottle(err)):
			s.sleep(time.Duration(attempt*50) * time.Millisecond)
		default:
			return fmt.Errorf("store: raise %s: %w", attr, err)
		}
	}
}

func isTxConflict(err error) bool {
	var c *types.TransactionConflictException
	return errors.As(err, &c)
}

type txClass int

const (
	txOther txClass = iota
	txDuplicate
	txThrottled
	txConflict
)

func classifyTx(err error) txClass {
	var tce *types.TransactionCanceledException
	if errors.As(err, &tce) {
		codes := cancellationCodes(tce)
		if len(codes) > 0 && codes[0] == "ConditionalCheckFailed" { // item 0 is the SEG put
			return txDuplicate
		}
		for _, c := range codes {
			switch c {
			case "ProvisionedThroughputExceeded", "ThrottlingError":
				return txThrottled
			case "TransactionConflict":
				return txConflict
			}
		}
		return txOther
	}
	if IsThrottle(err) {
		return txThrottled
	}
	return txOther
}

// cancellationCodes reads the per-item reasons, falling back to the
// "[ConditionalCheckFailed, None, None]" list in the message when they're absent.
func cancellationCodes(tce *types.TransactionCanceledException) []string {
	codes := make([]string, 0, len(tce.CancellationReasons))
	for _, r := range tce.CancellationReasons {
		codes = append(codes, aws.ToString(r.Code))
	}
	if len(codes) == 0 {
		msg := aws.ToString(tce.Message)
		if i, j := strings.LastIndex(msg, "["), strings.LastIndex(msg, "]"); i >= 0 && j > i {
			for _, c := range strings.Split(msg[i+1:j], ",") {
				codes = append(codes, strings.TrimSpace(c))
			}
		}
	}
	return codes
}
