// Package mongostore keeps fausto's proposals and tasks in MongoDB, shared
// by everyone's fausto and the agents' `fausto task` commands.
package mongostore

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/JuanVizcaya/fausto/internal/store"
)

// Store is a store.Store backed by a MongoDB database.
type Store struct {
	client    *mongo.Client
	proposals *mongo.Collection
	tasks     *mongo.Collection
}

var _ store.Store = (*Store)(nil)

// FromEnv connects to FAUSTO_MONGO_URI (database FAUSTO_MONGO_DB, default
// "fausto"). It returns nil, nil when FAUSTO_MONGO_URI is unset.
func FromEnv(ctx context.Context) (*Store, error) {
	uri := os.Getenv("FAUSTO_MONGO_URI")
	if uri == "" {
		return nil, nil
	}
	db := os.Getenv("FAUSTO_MONGO_DB")
	if db == "" {
		db = "fausto"
	}
	return Open(ctx, uri, db)
}

// Open connects to the database db at uri, checks it answers, and ensures
// its indexes.
func Open(ctx context.Context, uri, db string) (*Store, error) {
	client, err := mongo.Connect(options.Client().ApplyURI(uri).SetTimeout(15 * time.Second).SetAppName("fausto"))
	if err != nil {
		return nil, fmt.Errorf("connect to MongoDB: %w", err)
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := client.Ping(ctx, nil); err != nil {
		_ = client.Disconnect(context.Background())
		return nil, fmt.Errorf("reach MongoDB: %w", err)
	}
	d := client.Database(db)
	s := &Store{client: client, proposals: d.Collection("proposals"), tasks: d.Collection("tasks")}
	_, err = s.tasks.Indexes().CreateMany(ctx, []mongo.IndexModel{
		{Keys: bson.D{{Key: "agent", Value: 1}, {Key: "status", Value: 1}}},
		{Keys: bson.D{{Key: "milestone", Value: 1}}},
		{Keys: bson.D{{Key: "proposal", Value: 1}}},
	})
	if err != nil {
		_ = client.Disconnect(context.Background())
		return nil, fmt.Errorf("create MongoDB indexes: %w", err)
	}
	return s, nil
}

func (s *Store) Close(ctx context.Context) error { return s.client.Disconnect(ctx) }

func now() time.Time { return time.Now().UTC().Truncate(time.Millisecond) }

func (s *Store) CreateProposal(ctx context.Context, p *store.Proposal) error {
	if p.Updated.IsZero() {
		p.Updated = now()
	}
	_, err := s.proposals.InsertOne(ctx, p)
	if mongo.IsDuplicateKeyError(err) {
		return store.ErrExists
	}
	return err
}

func (s *Store) Proposal(ctx context.Context, id string) (*store.Proposal, error) {
	var p store.Proposal
	err := s.proposals.FindOne(ctx, bson.D{{Key: "_id", Value: id}}).Decode(&p)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, store.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &p, nil
}

func (s *Store) Proposals(ctx context.Context) ([]store.Proposal, error) {
	cur, err := s.proposals.Find(ctx, bson.D{})
	if err != nil {
		return nil, err
	}
	out := []store.Proposal{}
	if err := cur.All(ctx, &out); err != nil {
		return nil, err
	}
	store.SortProposals(out)
	return out, nil
}

func (s *Store) CloseProposal(ctx context.Context, id, status, date, commit string, tasks []string) error {
	set := bson.D{{Key: "status", Value: status}, {Key: "commit", Value: commit}, {Key: "tasks", Value: tasks}, {Key: "updated", Value: now()}}
	if status == "accepted" {
		set = append(set, bson.E{Key: "accepted", Value: date})
	}
	return s.conditional(ctx, s.proposals, id, bson.D{{Key: "status", Value: "open"}}, bson.D{{Key: "$set", Value: set}})
}

// CreateTasks inserts tasks after checking none of their ids is taken.
// Ids are generated from the proposal, so a clash means a retried accept.
func (s *Store) CreateTasks(ctx context.Context, tasks []store.Task) error {
	if len(tasks) == 0 {
		return nil
	}
	ids := make([]string, len(tasks))
	docs := make([]any, len(tasks))
	for i := range tasks {
		if tasks[i].Updated.IsZero() {
			tasks[i].Updated = now()
		}
		ids[i], docs[i] = tasks[i].ID, tasks[i]
	}
	n, err := s.tasks.CountDocuments(ctx, bson.D{{Key: "_id", Value: bson.D{{Key: "$in", Value: ids}}}})
	if err != nil {
		return err
	}
	if n > 0 {
		return store.ErrExists
	}
	_, err = s.tasks.InsertMany(ctx, docs)
	if mongo.IsDuplicateKeyError(err) {
		return store.ErrExists
	}
	return err
}

func (s *Store) Task(ctx context.Context, id string) (*store.Task, error) {
	var t store.Task
	err := s.tasks.FindOne(ctx, bson.D{{Key: "_id", Value: id}}).Decode(&t)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, store.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &t, nil
}

func (s *Store) Tasks(ctx context.Context, f store.TaskFilter) ([]store.Task, error) {
	filter := bson.D{}
	if len(f.IDs) > 0 {
		filter = append(filter, bson.E{Key: "_id", Value: bson.D{{Key: "$in", Value: f.IDs}}})
	}
	if f.Agent != "" {
		filter = append(filter, bson.E{Key: "agent", Value: f.Agent})
	}
	if f.Milestone != "" {
		filter = append(filter, bson.E{Key: "milestone", Value: f.Milestone})
	}
	if len(f.Statuses) > 0 {
		filter = append(filter, bson.E{Key: "status", Value: bson.D{{Key: "$in", Value: f.Statuses}}})
	}
	cur, err := s.tasks.Find(ctx, filter)
	if err != nil {
		return nil, err
	}
	out := []store.Task{}
	if err := cur.All(ctx, &out); err != nil {
		return nil, err
	}
	store.SortTasks(out)
	return out, nil
}

func (s *Store) SetTaskStatus(ctx context.Context, id, from, to, by string) error {
	t := now()
	update := bson.D{
		{Key: "$set", Value: bson.D{{Key: "status", Value: to}, {Key: "updated", Value: t}}},
		{Key: "$push", Value: bson.D{{Key: "history", Value: store.StatusChange{At: t, By: by, From: from, To: to}}}},
	}
	return s.conditional(ctx, s.tasks, id, bson.D{{Key: "status", Value: from}}, update)
}

func (s *Store) SetTaskJira(ctx context.Context, id, key string) error {
	res, err := s.tasks.UpdateOne(ctx, bson.D{{Key: "_id", Value: id}},
		bson.D{{Key: "$set", Value: bson.D{{Key: "jira", Value: key}, {Key: "updated", Value: now()}}}})
	if err != nil {
		return err
	}
	if res.MatchedCount == 0 {
		return store.ErrNotFound
	}
	return nil
}

// conditional applies update to document id if it also matches cond:
// ErrNotFound if there is no such document, ErrChanged if cond fails.
func (s *Store) conditional(ctx context.Context, c *mongo.Collection, id string, cond, update bson.D) error {
	filter := append(bson.D{{Key: "_id", Value: id}}, cond...)
	res, err := c.UpdateOne(ctx, filter, update)
	if err != nil {
		return err
	}
	if res.MatchedCount == 1 {
		return nil
	}
	n, err := c.CountDocuments(ctx, bson.D{{Key: "_id", Value: id}})
	if err != nil {
		return err
	}
	if n == 0 {
		return store.ErrNotFound
	}
	return store.ErrChanged
}
