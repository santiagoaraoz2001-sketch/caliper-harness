// Package execution adapts the schema to the handwritten state resolvers.
package execution

import (
	"context"
	"specific-harness/internal/graph/model"
)

type ResolverRoot interface {
	Mutation() MutationResolver
	Query() QueryResolver
}

type MutationResolver interface {
	RequestRevision(ctx context.Context, kind string, id string, expectedSnapshot string, text string) (*model.Revision, error)
	EditRevision(ctx context.Context, kind string, id string, expectedSnapshot string, field string, value string) (*model.RevisionContract, error)
	ResolveRevision(ctx context.Context, kind string, id string, revisionID string, expectedSnapshot string) (*model.Revision, error)
	AddTaskComment(ctx context.Context, taskID string, expectedAttempt int, text string) (*model.TaskComment, error)
	PrepareTask(ctx context.Context, input model.FreshTaskInput) (*model.PreparedTask, error)
	AppendKnowledge(ctx context.Context, key string, body string, expectedPreviousID string, taskID *string, filePath *string, references []*model.KnowledgeReferenceInput) (*model.Knowledge, error)
	Noticed(ctx context.Context, approachID string, body string, filePath *string) (*model.Knowledge, error)
	PutKnowledge(ctx context.Context, key string, body string, expectedPreviousID string, taskID *string, filePath *string) (*model.Knowledge, error)
	BindPlanProject(ctx context.Context, id string) (*model.Plan, error)
	AddMemory(ctx context.Context, kind string, body string, sessionRef string) (*model.MemoryEntry, error)
	AddSessionEvent(ctx context.Context, sessionID string, kind string, text string) (*model.SessionEvent, error)
	CreatePlan(ctx context.Context, title string, goal string) (*model.Plan, error)
	CreateApproach(ctx context.Context, planID string, title string) (*model.Approach, error)
	CreateTask(ctx context.Context, approachID string, title string, prompt string) (*model.Task, error)
	TransitionPlan(ctx context.Context, id string, to string) (*model.Plan, error)
	TransitionApproach(ctx context.Context, id string, to string) (*model.Approach, error)
	TransitionTask(ctx context.Context, id string, to string) (*model.Task, error)
	SetTaskBlock(ctx context.Context, id string, current string, target string, sketch string, acceptance string, tests string, verify string, checkCommand *string, acceptanceCommand *string) (*model.Task, error)
	RetirePlan(ctx context.Context, id string) (*model.Plan, error)
	RetireApproach(ctx context.Context, id string) (*model.Approach, error)
}
type QueryResolver interface {
	SessionEvidence(ctx context.Context, sessionID string, offset int, limit int, callID *string) (*model.EvidencePage, error)
	TaskEvidence(ctx context.Context, taskID string, offset int, limit int, attempt *int, kind *string, record *int, phase *string, callID *string) (*model.EvidencePage, error)
	RevisionContract(ctx context.Context, kind string, id string) (*model.RevisionContract, error)
	TaskComments(ctx context.Context, taskID string, attempt int, limit int) ([]*model.TaskComment, error)
	Knowledge(ctx context.Context, key *string, id *string, limit *int, text *string, section *string, offset *int, linkedFrom *string, linkedTo *string, relation *string) ([]*model.Knowledge, error)
	Notices(ctx context.Context, approachID string, limit *int) ([]*model.Knowledge, error)
	Version(ctx context.Context) (string, error)
	Memories(ctx context.Context, kind *string, text *string, limit *int) ([]*model.MemoryEntry, error)
	Sessions(ctx context.Context) ([]*model.Session, error)
	Session(ctx context.Context, id string) (*model.Session, error)
	Plan(ctx context.Context, id string) (*model.Plan, error)
	Plans(ctx context.Context, state *string) ([]*model.Plan, error)
	Approach(ctx context.Context, id string) (*model.Approach, error)
	Task(ctx context.Context, id string) (*model.Task, error)
}
