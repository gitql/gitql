package gitbase

import (
	"io"
	"strconv"

	"gopkg.in/src-d/go-mysql-server.v0/sql"
	"gopkg.in/src-d/go-mysql-server.v0/sql/expression"
	"gopkg.in/src-d/go-mysql-server.v0/sql/plan"

	"gopkg.in/src-d/go-git.v4/plumbing"
	"gopkg.in/src-d/go-git.v4/plumbing/object"
)

type treeEntriesTable struct{}

// TreeEntriesSchema is the schema for the tree entries table.
var TreeEntriesSchema = sql.Schema{
	{Name: "repository_id", Type: sql.Text, Nullable: false, Source: TreeEntriesTableName},
	{Name: "tree_entry_name", Type: sql.Text, Nullable: false, Source: TreeEntriesTableName},
	{Name: "blob_hash", Type: sql.Text, Nullable: false, Source: TreeEntriesTableName},
	{Name: "tree_hash", Type: sql.Text, Nullable: false, Source: TreeEntriesTableName},
	{Name: "tree_entry_mode", Type: sql.Text, Nullable: false, Source: TreeEntriesTableName},
}

var _ sql.PushdownProjectionAndFiltersTable = (*treeEntriesTable)(nil)

func newTreeEntriesTable() Indexable {
	return new(treeEntriesTable)
}

var _ Table = (*treeEntriesTable)(nil)

func (treeEntriesTable) isGitbaseTable() {}

func (treeEntriesTable) Resolved() bool {
	return true
}

func (treeEntriesTable) Name() string {
	return TreeEntriesTableName
}

func (treeEntriesTable) Schema() sql.Schema {
	return TreeEntriesSchema
}

func (r *treeEntriesTable) TransformUp(f sql.TransformNodeFunc) (sql.Node, error) {
	return f(r)
}

func (r *treeEntriesTable) TransformExpressionsUp(f sql.TransformExprFunc) (sql.Node, error) {
	return r, nil
}

func (r treeEntriesTable) RowIter(ctx *sql.Context) (sql.RowIter, error) {
	span, ctx := ctx.Span("gitbase.TreeEntriesTable")
	iter := new(treeEntryIter)

	repoIter, err := NewRowRepoIter(ctx, iter)
	if err != nil {
		span.Finish()
		return nil, err
	}

	return sql.NewSpanIter(span, repoIter), nil
}

func (treeEntriesTable) Children() []sql.Node {
	return nil
}

func (treeEntriesTable) HandledFilters(filters []sql.Expression) []sql.Expression {
	return handledFilters(TreeEntriesTableName, TreeEntriesSchema, filters)
}

func (treeEntriesTable) handledColumns() []string {
	return []string{"tree_hash"}
}

func (r *treeEntriesTable) WithProjectAndFilters(
	ctx *sql.Context,
	_, filters []sql.Expression,
) (sql.RowIter, error) {
	span, ctx := ctx.Span("gitbase.TreeEntriesTable")
	// TODO: could be optimized even more checking that only tree_hash is
	// projected. There would be no need to iterate files in this case, and
	// it would be much faster.
	iter, err := rowIterWithSelectors(
		ctx, TreeEntriesSchema, TreeEntriesTableName,
		filters, nil,
		r.handledColumns(),
		treeEntriesIterBuilder,
	)

	if err != nil {
		span.Finish()
		return nil, err
	}

	return sql.NewSpanIter(span, iter), nil
}

// IndexKeyValueIter implements the sql.Indexable interface.
func (*treeEntriesTable) IndexKeyValueIter(
	ctx *sql.Context,
	colNames []string,
) (sql.IndexKeyValueIter, error) {
	s, ok := ctx.Session.(*Session)
	if !ok || s == nil {
		return nil, ErrInvalidGitbaseSession.New(ctx.Session)
	}

	return newTreeEntriesKeyValueIter(s.Pool, colNames), nil
}

// WithProjectFiltersAndIndex implements sql.Indexable interface.
func (*treeEntriesTable) WithProjectFiltersAndIndex(
	ctx *sql.Context,
	columns, filters []sql.Expression,
	index sql.IndexValueIter,
) (sql.RowIter, error) {
	span, ctx := ctx.Span("gitbase.TreeEntriesTable.WithProjectFiltersAndIndex")
	s, ok := ctx.Session.(*Session)
	if !ok || s == nil {
		span.Finish()
		return nil, ErrInvalidGitbaseSession.New(ctx.Session)
	}

	session, err := getSession(ctx)
	if err != nil {
		return nil, err
	}

	var iter sql.RowIter = &treeEntriesIndexIter{index: index, pool: session.Pool}

	if len(filters) > 0 {
		iter = plan.NewFilterIter(ctx, expression.JoinAnd(filters...), iter)
	}

	return sql.NewSpanIter(span, iter), nil
}

func treeEntriesIterBuilder(_ *sql.Context, selectors selectors, _ []sql.Expression) (RowRepoIter, error) {
	if len(selectors["tree_hash"]) == 0 {
		return new(treeEntryIter), nil
	}

	hashes, err := selectors.textValues("tree_hash")
	if err != nil {
		return nil, err
	}

	return &treeEntriesByHashIter{hashes: hashes}, nil
}

func (r treeEntriesTable) String() string {
	return printTable(TreeEntriesTableName, TreeEntriesSchema)
}

type treeEntryIter struct {
	i      *object.TreeIter
	tree   *object.Tree
	cursor int
	repoID string
}

func (i *treeEntryIter) NewIterator(repo *Repository) (RowRepoIter, error) {
	iter, err := repo.Repo.TreeObjects()
	if err != nil {
		return nil, err
	}

	return &treeEntryIter{repoID: repo.ID, i: iter}, nil
}

func (i *treeEntryIter) Next() (sql.Row, error) {
	for {
		if i.tree == nil {
			var err error
			i.tree, err = i.i.Next()
			if err != nil {
				return nil, err
			}

			i.cursor = 0
		}

		if i.cursor >= len(i.tree.Entries) {
			i.tree = nil
			continue
		}

		entry := &TreeEntry{i.tree.Hash, i.tree.Entries[i.cursor]}
		i.cursor++

		return treeEntryToRow(i.repoID, entry), nil
	}
}

func (i *treeEntryIter) Close() error {
	if i.i != nil {
		i.i.Close()
	}

	return nil
}

type treeEntriesByHashIter struct {
	hashes []string
	pos    int
	tree   *object.Tree
	cursor int
	repo   *Repository
}

func (i *treeEntriesByHashIter) NewIterator(repo *Repository) (RowRepoIter, error) {
	return &treeEntriesByHashIter{hashes: i.hashes, repo: repo}, nil
}

func (i *treeEntriesByHashIter) Next() (sql.Row, error) {
	for {
		if i.pos >= len(i.hashes) && i.tree == nil {
			return nil, io.EOF
		}

		if i.tree == nil {
			hash := plumbing.NewHash(i.hashes[i.pos])
			i.pos++
			var err error
			i.tree, err = i.repo.Repo.TreeObject(hash)
			if err != nil {
				if err == plumbing.ErrObjectNotFound {
					continue
				}
				return nil, err
			}

			i.cursor = 0
		}

		if i.cursor >= len(i.tree.Entries) {
			i.tree = nil
			continue
		}

		entry := &TreeEntry{i.tree.Hash, i.tree.Entries[i.cursor]}
		i.cursor++

		return treeEntryToRow(i.repo.ID, entry), nil
	}
}

func (i *treeEntriesByHashIter) Close() error {
	return nil
}

// TreeEntry is a tree entry object.
type TreeEntry struct {
	TreeHash plumbing.Hash
	object.TreeEntry
}

func treeEntryToRow(repoID string, entry *TreeEntry) sql.Row {
	return sql.NewRow(
		repoID,
		entry.Name,
		entry.Hash.String(),
		entry.TreeHash.String(),
		strconv.FormatInt(int64(entry.Mode), 8),
	)
}

type treeEntriesIndexKey struct {
	Repository string
	Packfile   string
	Offset     int64
	Pos        int
}

type treeEntriesKeyValueIter struct {
	iter    *objectIter
	obj     *encodedObject
	tree    *object.Tree
	pos     int
	columns []string
}

func newTreeEntriesKeyValueIter(pool *RepositoryPool, columns []string) *treeEntriesKeyValueIter {
	return &treeEntriesKeyValueIter{
		iter:    newObjectIter(pool, plumbing.TreeObject),
		columns: columns,
	}
}

func (i *treeEntriesKeyValueIter) Next() ([]interface{}, []byte, error) {
	for {
		if i.tree == nil {
			var err error
			i.obj, err = i.iter.Next()
			if err != nil {
				return nil, nil, err
			}

			var ok bool
			i.tree, ok = i.obj.Object.(*object.Tree)
			if !ok {
				ErrInvalidObjectType.New(i.obj.Object, "*object.Tree")
			}

			i.pos = 0
		}

		if i.pos >= len(i.tree.Entries) {
			i.tree = nil
			continue
		}

		entry := i.tree.Entries[i.pos]
		i.pos++

		key, err := encodeIndexKey(treeEntriesIndexKey{
			Repository: i.obj.RepositoryID,
			Packfile:   i.obj.Packfile.String(),
			Offset:     int64(i.obj.Offset),
			Pos:        i.pos - 1,
		})
		if err != nil {
			return nil, nil, err
		}

		row := treeEntryToRow(i.obj.RepositoryID, &TreeEntry{i.tree.Hash, entry})
		values, err := rowIndexValues(row, i.columns, TreeEntriesSchema)
		if err != nil {
			return nil, nil, err
		}

		return values, key, nil
	}
}

func (i *treeEntriesKeyValueIter) Close() error { return i.iter.Close() }

type treeEntriesIndexIter struct {
	index          sql.IndexValueIter
	pool           *RepositoryPool
	decoder        *objectDecoder
	prevTreeOffset int64
	tree           *object.Tree
}

func (i *treeEntriesIndexIter) Next() (sql.Row, error) {
	data, err := i.index.Next()
	if err != nil {
		return nil, err
	}

	var key treeEntriesIndexKey
	if err := decodeIndexKey(data, &key); err != nil {
		return nil, err
	}

	packfile := plumbing.NewHash(key.Packfile)
	if i.decoder == nil || !i.decoder.equals(key.Repository, packfile) {
		if i.decoder != nil {
			if err := i.decoder.Close(); err != nil {
				return nil, err
			}
		}

		i.decoder, err = newObjectDecoder(i.pool.repositories[key.Repository], packfile)
		if err != nil {
			return nil, err
		}
	}

	var tree *object.Tree
	if i.prevTreeOffset == key.Offset {
		tree = i.tree
	} else {
		obj, err := i.decoder.get(key.Offset)
		if err != nil {
			return nil, err
		}

		var ok bool
		i.tree, ok = obj.(*object.Tree)
		if !ok {
			return nil, ErrInvalidObjectType.New(obj, "*object.Tree")
		}

		tree = i.tree
	}

	i.prevTreeOffset = key.Offset
	entry := &TreeEntry{tree.Hash, tree.Entries[key.Pos]}
	return treeEntryToRow(key.Repository, entry), nil
}

func (i *treeEntriesIndexIter) Close() error {
	if i.decoder != nil {
		if err := i.decoder.Close(); err != nil {
			_ = i.index.Close()
			return err
		}
	}

	return i.index.Close()
}
