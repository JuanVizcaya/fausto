// Package mongostore keeps fausto's projects, and each project's proposals
// and tasks, in fausto's MongoDB database.
package mongostore

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
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
	tokens   *mongo.Collection
	indexed  sync.Map // project id -> struct{}, once its indexes exist
}

var _ store.Projects = (*DB)(nil)

// Store is a store.Store for one project's collections.
type Store struct {
	proposals *mongo.Collection
	tasks     *mongo.Collection
	spec      *mongo.Collection
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
	return &DB{client: client, db: d, projects: d.Collection("projects"), tokens: d.Collection("tokens")}, nil
}

func (d *DB) Close(ctx context.Context) error { return d.client.Disconnect(ctx) }

// Store returns project id's proposals and tasks, creating the tasks'
// indexes the first time.
func (d *DB) Store(id string) store.Store {
	s := &Store{proposals: d.db.Collection(id + ".proposals"), tasks: d.db.Collection(id + ".tasks"), spec: d.db.Collection(id + ".spec")}
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

func (d *DB) CreateToken(ctx context.Context, t *store.Token) error {
	_, err := d.tokens.InsertOne(ctx, t)
	if mongo.IsDuplicateKeyError(err) {
		return store.ErrExists
	}
	return err
}

func (d *DB) TokenByHash(ctx context.Context, hash string) (*store.Token, error) {
	var t store.Token
	err := d.tokens.FindOne(ctx, bson.D{{Key: "_id", Value: hash}}).Decode(&t)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, store.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &t, nil
}

func (d *DB) Tokens(ctx context.Context, project string) ([]store.Token, error) {
	filter := bson.D{}
	if project != "" {
		filter = bson.D{{Key: "project", Value: project}}
	}
	cur, err := d.tokens.Find(ctx, filter, options.Find().SetSort(bson.D{{Key: "created", Value: 1}}))
	if err != nil {
		return nil, err
	}
	out := []store.Token{}
	if err := cur.All(ctx, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (d *DB) DeleteToken(ctx context.Context, label string) error {
	res, err := d.tokens.DeleteOne(ctx, bson.D{{Key: "label", Value: label}})
	if err != nil {
		return err
	}
	if res.DeletedCount == 0 {
		return store.ErrNotFound
	}
	return nil
}

// LegacyCharters returns the agent charters fausto kept for project id
// before it kept whole specs, as files by path (system/agents/<id>.yaml);
// none when there are none.
func (d *DB) LegacyCharters(ctx context.Context, id string) (map[string][]byte, error) {
	cur, err := d.db.Collection(id+".charters").Find(ctx, bson.D{})
	if err != nil {
		return nil, err
	}
	var docs []struct {
		ID   string `bson:"_id"`
		YAML string `bson:"yaml"`
	}
	if err := cur.All(ctx, &docs); err != nil {
		return nil, err
	}
	out := map[string][]byte{}
	for _, c := range docs {
		out["system/agents/"+c.ID+".yaml"] = []byte(c.YAML)
	}
	return out, nil
}

// DropLegacyCharters removes project id's old charters collection, once
// its charters are part of a spec revision.
func (d *DB) DropLegacyCharters(ctx context.Context, id string) error {
	return d.db.Collection(id + ".charters").Drop(ctx)
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

// RenameProject gives project from the id to: its collections
// ("<from>.proposals", ...) become to's, and so do its record and its
// agents' tokens. A display name equal to the old id becomes the new id. It
// reports which collections it renamed, and renames nothing when to exists.
func (d *DB) RenameProject(ctx context.Context, from, to string) ([]string, error) {
	if err := store.ValidProjectID(to); err != nil {
		return nil, err
	}
	p, err := d.Project(ctx, from)
	if err != nil {
		return nil, err
	}
	if _, err := d.Project(ctx, to); err == nil {
		return nil, store.ErrExists
	} else if !errors.Is(err, store.ErrNotFound) {
		return nil, err
	}
	names, err := d.db.ListCollectionNames(ctx, bson.D{})
	if err != nil {
		return nil, err
	}
	for _, n := range names {
		if strings.HasPrefix(n, to+".") {
			return nil, fmt.Errorf("collection %s: %w", n, store.ErrExists)
		}
	}
	var renamed []string
	for _, n := range names {
		rest, ok := strings.CutPrefix(n, from+".")
		if !ok {
			continue
		}
		target := to + "." + rest
		cmd := bson.D{{Key: "renameCollection", Value: d.db.Name() + "." + n}, {Key: "to", Value: d.db.Name() + "." + target}}
		if err := d.client.Database("admin").RunCommand(ctx, cmd).Err(); err != nil {
			return renamed, fmt.Errorf("rename %s to %s: %w", n, target, err)
		}
		renamed = append(renamed, n+" → "+target)
	}
	d.indexed.Delete(from)
	p.ID = to
	if p.Name == from {
		p.Name = to
	}
	if err := d.PutProject(ctx, p); err != nil {
		return renamed, err
	}
	if _, err := d.projects.DeleteOne(ctx, bson.D{{Key: "_id", Value: from}}); err != nil {
		return renamed, err
	}
	_, err = d.tokens.UpdateMany(ctx, bson.D{{Key: "project", Value: from}}, bson.D{{Key: "$set", Value: bson.D{{Key: "project", Value: to}}}})
	return renamed, err
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
	add("specDiff", p.SpecDiff)
	if p.Revision != 0 {
		set = append(set, bson.E{Key: "revision", Value: p.Revision})
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

func (s *Store) SpecRevision(ctx context.Context, n int) (*store.SpecRevision, error) {
	var r store.SpecRevision
	var err error
	if n == 0 {
		err = s.spec.FindOne(ctx, bson.D{}, options.FindOne().SetSort(bson.D{{Key: "_id", Value: -1}})).Decode(&r)
	} else {
		err = s.spec.FindOne(ctx, bson.D{{Key: "_id", Value: n}}).Decode(&r)
	}
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, store.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &r, nil
}

// AddSpecRevision inserts r once r.N follows the latest revision; the
// unique _id stops two writers adding the same number.
func (s *Store) AddSpecRevision(ctx context.Context, r *store.SpecRevision) error {
	latest := 0
	if cur, err := s.SpecRevision(ctx, 0); err == nil {
		latest = cur.N
	} else if !errors.Is(err, store.ErrNotFound) {
		return err
	}
	if r.N != latest+1 {
		return store.ErrChanged
	}
	if r.At.IsZero() {
		r.At = now()
	}
	_, err := s.spec.InsertOne(ctx, r)
	if mongo.IsDuplicateKeyError(err) {
		return store.ErrChanged
	}
	return err
}

func (s *Store) SpecRevisions(ctx context.Context) ([]store.SpecRevision, error) {
	cur, err := s.spec.Find(ctx, bson.D{}, options.Find().SetSort(bson.D{{Key: "_id", Value: -1}}).SetProjection(bson.D{{Key: "files", Value: 0}}))
	if err != nil {
		return nil, err
	}
	out := []store.SpecRevision{}
	if err := cur.All(ctx, &out); err != nil {
		return nil, err
	}
	return out, nil
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
