// Package mongostore keeps fausto's projects, and each project's proposals
// and tasks, in fausto's MongoDB database.
package mongostore

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"sync"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/JuanVizcaya/fausto/internal/store"
)

// DB is fausto's MongoDB database: the projects it works on, and each
// project's proposals and tasks in collections named after it
// ("<project>.proposals", "<project>.tasks").
type DB struct {
	client   *mongo.Client
	db       *mongo.Database
	projects *mongo.Collection
	indexed  sync.Map // project id -> struct{}, once its indexes exist
}

var _ store.Projects = (*DB)(nil)

// Store is a store.Store for one project's collections.
type Store struct {
	proposals *mongo.Collection
	tasks     *mongo.Collection
}

var _ store.Store = (*Store)(nil)

// FromEnv connects to FAUSTO_MONGO_URI (database FAUSTO_MONGO_DB, default
// "fausto"). It returns nil, nil when FAUSTO_MONGO_URI is unset.
func FromEnv(ctx context.Context) (*DB, error) {
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

// Open connects to the database db at uri and checks it answers.
func Open(ctx context.Context, uri, db string) (*DB, error) {
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
	return &DB{client: client, db: d, projects: d.Collection("projects")}, nil
}

func (d *DB) Close(ctx context.Context) error { return d.client.Disconnect(ctx) }

// Store returns project id's proposals and tasks, creating the tasks'
// indexes the first time.
func (d *DB) Store(id string) store.Store {
	s := &Store{proposals: d.db.Collection(id + ".proposals"), tasks: d.db.Collection(id + ".tasks")}
	if _, done := d.indexed.Load(id); !done {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, err := s.tasks.Indexes().CreateMany(ctx, []mongo.IndexModel{
			{Keys: bson.D{{Key: "agent", Value: 1}, {Key: "status", Value: 1}}},
			{Keys: bson.D{{Key: "milestone", Value: 1}}},
			{Keys: bson.D{{Key: "proposal", Value: 1}}},
		})
		if err == nil {
			d.indexed.Store(id, struct{}{})
		}
	}
	return s
}

func (d *DB) Projects(ctx context.Context) ([]store.Project, error) {
	cur, err := d.projects.Find(ctx, bson.D{})
	if err != nil {
		return nil, err
	}
	out := []store.Project{}
	if err := cur.All(ctx, &out); err != nil {
		return nil, err
	}
	store.SortProjects(out)
	return out, nil
}

func (d *DB) Project(ctx context.Context, id string) (*store.Project, error) {
	var p store.Project
	err := d.projects.FindOne(ctx, bson.D{{Key: "_id", Value: id}}).Decode(&p)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, store.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &p, nil
}

func (d *DB) PutProject(ctx context.Context, p *store.Project) error {
	p.Updated = now()
	_, err := d.projects.ReplaceOne(ctx, bson.D{{Key: "_id", Value: p.ID}}, p, options.Replace().SetUpsert(true))
	return err
}

func (d *DB) DeleteProject(ctx context.Context, id string) error {
	res, err := d.projects.DeleteOne(ctx, bson.D{{Key: "_id", Value: id}})
	if err != nil {
		return err
	}
	if res.DeletedCount == 0 {
		return store.ErrNotFound
	}
	return nil
}

// AdoptLegacy renames the collections fausto used before it had projects
// ("proposals", "tasks") to project id's, so that project keeps them. It
// reports which it renamed; it renames nothing whose target already exists.
func (d *DB) AdoptLegacy(ctx context.Context, id string) ([]string, error) {
	names, err := d.db.ListCollectionNames(ctx, bson.D{})
	if err != nil {
		return nil, err
	}
	var renamed []string
	for _, c := range []string{"proposals", "tasks"} {
		target := id + "." + c
		if !slices.Contains(names, c) || slices.Contains(names, target) {
			continue
		}
		cmd := bson.D{{Key: "renameCollection", Value: d.db.Name() + "." + c}, {Key: "to", Value: d.db.Name() + "." + target}}
		if err := d.client.Database("admin").RunCommand(ctx, cmd).Err(); err != nil {
			return renamed, fmt.Errorf("rename %s to %s: %w", c, target, err)
		}
		renamed = append(renamed, c+" → "+target)
	}
	return renamed, nil
}

// Close does nothing: the connection belongs to the DB.
func (s *Store) Close(context.Context) error { return nil }

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

func (s *Store) MoveProposal(ctx context.Context, id, from, to string, u store.ProposalUpdate) error {
	if err := store.ValidMove(from, to); err != nil {
		return err
	}
	// The fields Apply sets, read back from a scratch proposal.
	var p store.Proposal
	u.Apply(&p, to)
	set := bson.D{{Key: "status", Value: to}, {Key: "updated", Value: now()}}
	add := func(key, v string) {
		if v != "" {
			set = append(set, bson.E{Key: key, Value: v})
		}
	}
	add("accepted", p.Accepted)
	add("applied", p.Applied)
	add("closed", p.Closed)
	add("commit", p.Commit)
	add("branch", p.Branch)
	add("url", p.URL)
	add("merged", p.Merged)
	if p.PR != 0 {
		set = append(set, bson.E{Key: "pr", Value: p.PR})
	}
	if u.Tasks != nil {
		set = append(set, bson.E{Key: "tasks", Value: p.Tasks})
	}
	if u.PendingTasks != nil {
		set = append(set, bson.E{Key: "pendingTasks", Value: p.PendingTasks})
	}
	return s.conditional(ctx, s.proposals, id, bson.D{{Key: "status", Value: from}}, bson.D{{Key: "$set", Value: set}})
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
