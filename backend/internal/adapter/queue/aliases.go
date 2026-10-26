// Package queue sends notion-sync.fifo messages over SQS (or in-process locally) and
// consumes them. The transport-free message code lives in queue/msg; these aliases
// keep queue.X callers compiling unchanged.
package queue

import "reading-tracker/backend/internal/adapter/queue/msg"

type (
	Message   = msg.Message
	Outgoing  = msg.Outgoing
	Sender    = msg.Sender
	SyncQueue = msg.SyncQueue
	Recorder  = msg.Recorder
	Type      = msg.Type
)

const (
	TypeArticle = msg.TypeArticle
	TypeDay     = msg.TypeDay
	TypePurge   = msg.TypePurge
)

var (
	Article  = msg.Article
	Day      = msg.Day
	Purge    = msg.Purge
	Bucketed = msg.Bucketed
)
