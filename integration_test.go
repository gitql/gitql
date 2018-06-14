package gitbase_test

import (
	"context"
	"io/ioutil"
	"os"
	"path/filepath"
	"testing"

	"github.com/src-d/gitbase/internal/rule"

	"github.com/src-d/gitbase"
	"github.com/src-d/gitbase/internal/function"
	"github.com/stretchr/testify/require"
	fixtures "gopkg.in/src-d/go-git-fixtures.v3"
	sqle "gopkg.in/src-d/go-mysql-server.v0"
	"gopkg.in/src-d/go-mysql-server.v0/sql"
	"gopkg.in/src-d/go-mysql-server.v0/sql/expression"
	"gopkg.in/src-d/go-mysql-server.v0/sql/index/pilosa"
)

func TestIntegration(t *testing.T) {
	engine := sqle.New()
	require.NoError(t, fixtures.Init())
	defer func() {
		require.NoError(t, fixtures.Clean())
	}()

	path := fixtures.ByTag("worktree").One().Worktree().Root()

	pool := gitbase.NewRepositoryPool()
	_, err := pool.AddGit(path)
	require.NoError(t, err)

	engine.AddDatabase(gitbase.NewDatabase("foo"))
	engine.Catalog.RegisterFunctions(function.Functions)

	testCases := []struct {
		query  string
		result []sql.Row
	}{
		{
			`SELECT COUNT(c.commit_hash), c.commit_hash
			FROM ref_commits r
			INNER JOIN commit_blobs c
				ON r.ref_name = 'HEAD' AND r.commit_hash = c.commit_hash
			INNER JOIN blobs b
				ON c.blob_hash = b.blob_hash
			GROUP BY c.commit_hash`,
			[]sql.Row{
				{int32(4), "1669dce138d9b841a518c64b10914d88f5e488ea"},
				{int32(3), "35e85108805c84807bc66a02d91535e1e24b38b9"},
				{int32(9), "6ecf0ef2c2dffb796033e5a02219af86ec6584e5"},
				{int32(8), "918c48b83bd081e863dbe1b80f8998f058cd8294"},
				{int32(3), "a5b8b09e2f8fcb0bb99d3ccb0958157b40890d69"},
				{int32(6), "af2d6a6954d532f8ffb47615169c8fdf9d383a1a"},
				{int32(2), "b029517f6300c2da0f4b651b8642506cd6aaf45d"},
				{int32(3), "b8e471f58bcbca63b07bda20e428190409c2db47"},
			},
		},
		{
			`SELECT ref_name FROM refs ORDER BY ref_name`,
			[]sql.Row{
				{"HEAD"},
				{"refs/heads/master"},
				{"refs/remotes/origin/branch"},
				{"refs/remotes/origin/master"},
			},
		},
		{
			`SELECT c.commit_hash
			FROM ref_commits r 
			INNER JOIN commits c 
				ON r.ref_name = 'HEAD' 
				AND r.commit_hash = c.commit_hash`,
			[]sql.Row{
				{"6ecf0ef2c2dffb796033e5a02219af86ec6584e5"},
				{"918c48b83bd081e863dbe1b80f8998f058cd8294"},
				{"af2d6a6954d532f8ffb47615169c8fdf9d383a1a"},
				{"1669dce138d9b841a518c64b10914d88f5e488ea"},
				{"a5b8b09e2f8fcb0bb99d3ccb0958157b40890d69"},
				{"b8e471f58bcbca63b07bda20e428190409c2db47"},
				{"35e85108805c84807bc66a02d91535e1e24b38b9"},
				{"b029517f6300c2da0f4b651b8642506cd6aaf45d"},
			},
		},
		{
			`SELECT COUNT(first_commit_year), first_commit_year
			FROM (
				SELECT YEAR(c.commit_author_when) AS first_commit_year
				FROM ref_commits rc
				INNER JOIN commits c 
					ON rc.commit_hash = c.commit_hash
				ORDER BY c.commit_author_when 
				LIMIT 1
			) repo_years
			GROUP BY first_commit_year`,
			[]sql.Row{{int32(1), int32(2015)}},
		},
		{
			`SELECT COUNT(*) as num_commits, month, repo_id, committer_email
			FROM (
				SELECT
					MONTH(committer_when) as month,
					rc.repository_id as repo_id,
					committer_email
				FROM ref_commits rc
				INNER JOIN commits c ON rc.commit_hash = c.commit_hash
				WHERE YEAR(committer_when) = 2015 AND rc.ref_name = 'refs/heads/master'
			) as t
			GROUP BY committer_email, month, repo_id`,
			[]sql.Row{
				{int32(6), int32(3), path, "mcuadros@gmail.com"},
				{int32(1), int32(4), path, "mcuadros@gmail.com"},
				{int32(1), int32(3), path, "daniel@lordran.local"},
			},
		},
		{
			`SELECT * FROM (
				SELECT COUNT(c.commit_hash) AS num, c.commit_hash
				FROM ref_commits r
				INNER JOIN commits c
					ON r.commit_hash = c.commit_hash
				GROUP BY c.commit_hash
			) t WHERE num > 1`,
			[]sql.Row{
				{int32(3), "6ecf0ef2c2dffb796033e5a02219af86ec6584e5"},
				{int32(4), "918c48b83bd081e863dbe1b80f8998f058cd8294"},
				{int32(4), "af2d6a6954d532f8ffb47615169c8fdf9d383a1a"},
				{int32(4), "1669dce138d9b841a518c64b10914d88f5e488ea"},
				{int32(4), "a5b8b09e2f8fcb0bb99d3ccb0958157b40890d69"},
				{int32(4), "b8e471f58bcbca63b07bda20e428190409c2db47"},
				{int32(4), "35e85108805c84807bc66a02d91535e1e24b38b9"},
				{int32(4), "b029517f6300c2da0f4b651b8642506cd6aaf45d"},
			},
		},
	}

	runTests := func(t *testing.T) {
		for _, tt := range testCases {
			t.Run(tt.query, func(t *testing.T) {
				require := require.New(t)

				session := gitbase.NewSession(pool)
				ctx := sql.NewContext(context.TODO(), sql.WithSession(session))

				_, iter, err := engine.Query(ctx, tt.query)
				require.NoError(err)
				rows, err := sql.RowIterToRows(iter)
				require.NoError(err)
				require.ElementsMatch(tt.result, rows)
			})
		}
	}

	t.Run("without squash", runTests)

	engine.Analyzer.AddRule(rule.SquashJoinsRule, rule.SquashJoins)
	t.Run("with squash", runTests)
}

func TestUastQueries(t *testing.T) {
	require := require.New(t)

	engine, pool, cleanup := setup(t)
	defer cleanup()

	session := gitbase.NewSession(pool)
	ctx := sql.NewContext(context.TODO(), sql.WithSession(session))
	_, iter, err := engine.Query(ctx, `
		SELECT uast_xpath(uast(blob_content, language(tree_entry_name, blob_content)), '//*[@roleIdentifier]') as uast,
			tree_entry_name
		FROM tree_entries te
		INNER JOIN blobs b
		ON b.blob_hash = te.blob_hash
		WHERE te.tree_entry_name = 'crappy.php'`,
	)
	require.NoError(err)

	rows, err := sql.RowIterToRows(iter)
	require.NoError(err)
	require.Len(rows, 1)
}

func TestSquashCorrectness(t *testing.T) {
	engine, pool, cleanup := setup(t)
	defer cleanup()

	squashEngine := sqle.New()
	squashEngine.AddDatabase(gitbase.NewDatabase("foo"))
	squashEngine.Catalog.RegisterFunctions(function.Functions)
	squashEngine.Analyzer.AddRule(rule.SquashJoinsRule, rule.SquashJoins)

	queries := []string{
		`SELECT * FROM repositories`,
		`SELECT * FROM refs`,
		`SELECT * FROM remotes`,
		`SELECT * FROM commits`,
		`SELECT * FROM tree_entries`,
		`SELECT * FROM blobs`,
		`SELECT * FROM files`,
		`SELECT * FROM repositories r INNER JOIN refs ON r.repository_id = refs.repository_id`,
		`SELECT * FROM repositories r INNER JOIN remotes ON r.repository_id = remotes.repository_id`,
		`SELECT * FROM refs r INNER JOIN remotes re ON r.repository_id = re.repository_id`,
		`SELECT * FROM refs r INNER JOIN commits c ON r.commit_hash = c.commit_hash`,
		`SELECT * FROM ref_commits r INNER JOIN commits c ON r.commit_hash = c.commit_hash`,
		`SELECT * FROM refs r INNER JOIN commit_trees t ON r.commit_hash = t.commit_hash`,
		`SELECT * FROM refs r INNER JOIN commit_blobs b ON r.commit_hash = b.commit_hash`,
		`SELECT * FROM refs r
		INNER JOIN commit_blobs cb
			ON r.commit_hash = cb.commit_hash
		INNER JOIN blobs b
			ON cb.blob_hash = b.blob_hash`,
		`SELECT * FROM commits c INNER JOIN commit_trees t ON c.commit_hash = t.tree_hash`,
		`SELECT * FROM commits c INNER JOIN tree_entries te ON c.tree_hash = te.tree_hash`,
		`SELECT * FROM commits c
		INNER JOIN commit_blobs cb
			ON c.commit_hash = cb.commit_hash
		INNER JOIN blobs b
			ON cb.blob_hash = b.blob_hash`,
		`SELECT * FROM tree_entries te INNER JOIN blobs b ON te.blob_hash = b.blob_hash`,

		`SELECT * FROM repositories r
		INNER JOIN refs re
			ON r.repository_id = re.repository_id
		INNER JOIN commits c
			ON re.commit_hash = c.commit_hash
		WHERE re.ref_name = 'HEAD'`,

		`SELECT * FROM commits c
		INNER JOIN commit_trees t
			ON c.commit_hash = t.commit_hash
		INNER JOIN tree_entries te
			ON t.tree_hash = te.tree_hash
		INNER JOIN blobs b
			ON te.blob_hash = b.blob_hash
		WHERE te.tree_entry_name = 'LICENSE'`,

		`SELECT * FROM repositories,
		commits c INNER JOIN tree_entries te
			ON c.tree_hash = te.tree_hash`,

		`SELECT * FROM refs r
		INNER JOIN ref_commits c
			ON r.ref_name = c.ref_name 
			AND c.repository_id = r.repository_id`,

		`SELECT * FROM refs r
		INNER JOIN ref_commits c
			ON r.commit_hash = c.commit_hash
			AND r.ref_name = c.ref_name
			AND c.repository_id = r.repository_id`,
	}

	for _, q := range queries {
		t.Run(q, func(t *testing.T) {
			expected := queryResults(t, engine, pool, q)
			result := queryResults(t, squashEngine, pool, q)
			require.Len(t, result, len(expected))
			require.ElementsMatch(
				t,
				expected,
				result,
			)
		})
	}
}

func queryResults(
	t *testing.T,
	e *sqle.Engine,
	pool *gitbase.RepositoryPool,
	q string,
) []sql.Row {
	session := gitbase.NewSession(pool)
	ctx := sql.NewContext(context.TODO(), sql.WithSession(session))

	_, iter, err := e.Query(ctx, q)
	require.NoError(t, err)

	rows, err := sql.RowIterToRows(iter)
	require.NoError(t, err)

	return rows
}

func TestMissingHeadRefs(t *testing.T) {
	require := require.New(t)

	path := filepath.Join(
		os.Getenv("GOPATH"),
		"src", "github.com", "src-d", "gitbase",
		"_testdata",
	)

	pool := gitbase.NewRepositoryPool()
	require.NoError(pool.AddSivaDir(path))

	engine := sqle.New()
	engine.AddDatabase(gitbase.NewDatabase("foo"))

	session := gitbase.NewSession(pool)
	ctx := sql.NewContext(context.TODO(), sql.WithSession(session))
	_, iter, err := engine.Query(ctx, "SELECT * FROM refs")
	require.NoError(err)

	rows, err := sql.RowIterToRows(iter)
	require.NoError(err)
	require.Len(rows, 56)
}

func BenchmarkQueries(b *testing.B) {
	queries := []struct {
		name  string
		query string
	}{
		{
			"simple query",
			`SELECT * FROM repositories r
			INNER JOIN refs rr
			ON r.repository_id = rr.repository_id`,
		},
		{
			"select by specific id",
			`SELECT * FROM ref_commits r
			INNER JOIN commits c
				ON c.commit_hash = r.commit_hash
			WHERE c.commit_hash = '6ecf0ef2c2dffb796033e5a02219af86ec6584e5'
				AND r.ref_name = 'refs/heads/master'`,
		},
		{
			"select file by name",
			`SELECT * FROM files WHERE file_path = 'LICENSE'`,
		},
		{
			"select files by language",
			`SELECT * FROM files WHERE language(file_path, blob_content) = 'Go'`,
		},
		{
			"query with commit_blobs",
			`SELECT COUNT(c.commit_hash), c.commit_hash
			FROM ref_commits r
			INNER JOIN commit_blobs c
				ON r.ref_name = 'HEAD' AND r.commit_hash = c.commit_hash
			INNER JOIN blobs b
				ON c.blob_hash = b.blob_hash
			GROUP BY c.commit_hash`,
		},
		{
			"query with history_idx and 3 joins",
			`SELECT COUNT(first_commit_year), first_commit_year
			FROM (
				SELECT YEAR(c.commit_author_when) AS first_commit_year
				FROM ref_commits r
				INNER JOIN commits c
					ON r.commit_hash = c.commit_hash
				ORDER BY c.commit_author_when
				LIMIT 1
			) repo_years
			GROUP BY first_commit_year`,
		},
		{
			"query with history_idx",
			`SELECT * FROM (
				SELECT COUNT(c.commit_hash) AS num, c.commit_hash
				FROM ref_commits r
				INNER JOIN commits c
					ON r.commit_hash = c.commit_hash
				GROUP BY c.commit_hash
			) t WHERE num > 1`,
		},
		{
			"join tree entries and blobs",
			`SELECT * FROM tree_entries te
			INNER JOIN blobs b
			ON te.blob_hash = b.blob_hash`,
		},
		{
			"join tree entries and blobs with filters",
			`SELECT * FROM tree_entries te
			INNER JOIN blobs b
			ON te.blob_hash = b.blob_hash
			WHERE te.tree_entry_name = 'LICENSE'`,
		},
		{
			"join refs and blobs",
			`SELECT * FROM refs r
			INNER JOIN commit_blobs cb
				ON r.commit_hash = cb.commit_hash
			INNER JOIN blobs b
				ON cb.blob_hash = b.blob_hash`,
		},
		{
			"join refs and blobs with filters",
			`SELECT * FROM refs r
			INNER JOIN commit_blobs cb
				ON r.commit_hash = cb.commit_hash
			INNER JOIN blobs b
				ON cb.blob_hash = b.blob_hash
			WHERE r.ref_name = 'refs/heads/master'`,
		},
	}

	indexesEngine, pool, cleanup := setup(b)
	defer cleanup()

	tmpDir, err := ioutil.TempDir(os.TempDir(), "pilosa-idx-gitbase")
	require.NoError(b, err)
	defer os.RemoveAll(tmpDir)
	indexesEngine.Catalog.RegisterIndexDriver(pilosa.NewIndexDriver(tmpDir))

	ctx := sql.NewContext(
		context.TODO(),
		sql.WithSession(gitbase.NewSession(pool)),
	)

	engine := sqle.New()
	engine.AddDatabase(gitbase.NewDatabase("foo"))
	engine.Catalog.RegisterFunctions(function.Functions)

	squashEngine := sqle.New()
	squashEngine.AddDatabase(gitbase.NewDatabase("foo"))
	squashEngine.Catalog.RegisterFunctions(function.Functions)
	squashEngine.Analyzer.AddRule(rule.SquashJoinsRule, rule.SquashJoins)

	cleanupIndexes := createTestIndexes(b, indexesEngine, ctx)
	defer cleanupIndexes()

	for _, qq := range queries {
		b.Run(qq.name, func(b *testing.B) {
			b.Run("base", func(b *testing.B) {
				benchmarkQuery(b, qq.query, engine, ctx)
			})

			b.Run("indexes", func(b *testing.B) {
				benchmarkQuery(b, qq.query, indexesEngine, ctx)
			})

			b.Run("squash", func(b *testing.B) {
				benchmarkQuery(b, qq.query, squashEngine, ctx)
			})
		})
	}
}

func benchmarkQuery(b *testing.B, query string, engine *sqle.Engine, ctx *sql.Context) {
	for i := 0; i < b.N; i++ {
		_, rows, err := engine.Query(ctx, query)
		require.NoError(b, err)

		_, err = sql.RowIterToRows(rows)
		require.NoError(b, err)
	}
}

func TestIndexes(t *testing.T) {
	engine, pool, cleanup := setup(t)
	defer cleanup()

	tmpDir, err := ioutil.TempDir(os.TempDir(), "pilosa-idx-gitbase")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)
	engine.Catalog.RegisterIndexDriver(pilosa.NewIndexDriver(tmpDir))

	ctx := sql.NewContext(
		context.TODO(),
		sql.WithSession(gitbase.NewSession(pool)),
	)

	baseEngine := sqle.New()
	baseEngine.AddDatabase(gitbase.NewDatabase("foo"))
	baseEngine.Catalog.RegisterFunctions(function.Functions)

	cleanupIndexes := createTestIndexes(t, engine, ctx)
	defer cleanupIndexes()

	testCases := []string{
		`SELECT ref_name, commit_hash FROM refs WHERE ref_name = 'refs/heads/master'`,
		`SELECT remote_name, remote_push_url FROM remotes WHERE remote_name = 'origin'`,
		`SELECT commit_hash, commit_author_email FROM commits WHERE commit_hash = '918c48b83bd081e863dbe1b80f8998f058cd8294'`,
		`SELECT commit_hash, ref_name FROM ref_commits WHERE ref_name = 'refs/heads/master'`,
		`SELECT commit_hash, tree_hash FROM commit_trees WHERE commit_hash = '918c48b83bd081e863dbe1b80f8998f058cd8294'`,
		`SELECT commit_hash, blob_hash FROM commit_blobs WHERE commit_hash = '918c48b83bd081e863dbe1b80f8998f058cd8294'`,
		`SELECT tree_entry_name, blob_hash FROM tree_entries WHERE tree_entry_name = 'LICENSE'`,
		`SELECT blob_hash, blob_size FROM blobs WHERE blob_hash = 'd5c0f4ab811897cadf03aec358ae60d21f91c50d'`,
		`SELECT file_path, blob_hash FROM files WHERE file_path = 'LICENSE'`,
	}

	for _, tt := range testCases {
		t.Run(tt, func(t *testing.T) {
			require := require.New(t)

			_, iter, err := engine.Query(ctx, tt)
			require.NoError(err)

			rows, err := sql.RowIterToRows(iter)
			require.NoError(err)

			_, iter, err = baseEngine.Query(ctx, tt)
			require.NoError(err)

			expected, err := sql.RowIterToRows(iter)
			require.NoError(err)

			require.ElementsMatch(expected, rows)
		})
	}
}

func col(t testing.TB, schema sql.Schema, name string) sql.Expression {
	for i, col := range schema {
		if col.Name == name {
			return expression.NewGetFieldWithTable(i, col.Type, col.Source, col.Name, col.Nullable)
		}
	}

	t.Fatalf("unknown column %s in schema", name)
	return nil
}

type indexData struct {
	id          string
	expressions []sql.Expression
	table       sql.Table
	columns     []string
}

func createTestIndexes(t testing.TB, engine *sqle.Engine, ctx *sql.Context) func() {
	db, err := engine.Catalog.Database("foo")
	require.NoError(t, err)
	tables := db.Tables()

	var indexes = []indexData{
		{
			id:      "refs_idx",
			table:   tables[gitbase.ReferencesTableName],
			columns: []string{"ref_name"},
			expressions: []sql.Expression{
				col(t, gitbase.RefsSchema, "ref_name"),
			},
		},
		{
			id:      "remotes_idx",
			table:   tables[gitbase.RemotesTableName],
			columns: []string{"remote_name"},
			expressions: []sql.Expression{
				col(t, gitbase.RemotesSchema, "remote_name"),
			},
		},
		{
			id:      "ref_commits_idx",
			table:   tables[gitbase.RefCommitsTableName],
			columns: []string{"ref_name"},
			expressions: []sql.Expression{
				col(t, gitbase.RefCommitsSchema, "ref_name"),
			},
		},
		{
			id:      "commits_idx",
			table:   tables[gitbase.CommitsTableName],
			columns: []string{"commit_hash"},
			expressions: []sql.Expression{
				col(t, gitbase.CommitsSchema, "commit_hash"),
			},
		},
		{
			id:      "commit_trees_idx",
			table:   tables[gitbase.CommitTreesTableName],
			columns: []string{"commit_hash"},
			expressions: []sql.Expression{
				col(t, gitbase.CommitTreesSchema, "commit_hash"),
			},
		},
		{
			id:      "commit_blobs_idx",
			table:   tables[gitbase.CommitBlobsTableName],
			columns: []string{"commit_hash"},
			expressions: []sql.Expression{
				col(t, gitbase.CommitBlobsSchema, "commit_hash"),
			},
		},
		{
			id:      "tree_entries_idx",
			table:   tables[gitbase.TreeEntriesTableName],
			columns: []string{"tree_entry_name"},
			expressions: []sql.Expression{
				col(t, gitbase.TreeEntriesSchema, "tree_entry_name"),
			},
		},
		{
			id:      "blobs_idx",
			table:   tables[gitbase.BlobsTableName],
			columns: []string{"blob_hash"},
			expressions: []sql.Expression{
				col(t, gitbase.BlobsSchema, "blob_hash"),
			},
		},
		{
			id:      "files_idx",
			table:   tables[gitbase.FilesTableName],
			columns: []string{"file_path"},
			expressions: []sql.Expression{
				col(t, gitbase.FilesSchema, "file_path"),
			},
		},
		{
			id:      "files_lang_idx",
			table:   tables[gitbase.FilesTableName],
			columns: []string{"file_path"},
			expressions: []sql.Expression{
				func() sql.Expression {
					f, _ := function.NewLanguage(
						col(t, gitbase.FilesSchema, "file_path"),
						col(t, gitbase.FilesSchema, "blob_content"),
					)
					return f
				}(),
			},
		},
	}

	for _, idx := range indexes {
		createIndex(t, engine, idx, ctx)
	}

	return func() {
		for _, idx := range indexes {
			defer deleteIndex(t, engine, idx)
		}
	}
}

func createIndex(
	t testing.TB,
	e *sqle.Engine,
	data indexData,
	ctx *sql.Context,
) {
	t.Helper()
	require := require.New(t)
	driver := e.Catalog.IndexDriver(pilosa.DriverID)
	require.NotNil(driver)

	var hashes []sql.ExpressionHash
	for _, e := range data.expressions {
		hashes = append(hashes, sql.NewExpressionHash(e))
	}

	idx, err := driver.Create(
		"foo", data.table.Name(),
		data.id, hashes,
		make(map[string]string),
	)
	require.NoError(err)

	done, err := e.Catalog.AddIndex(idx)
	require.NoError(err)

	iter, err := data.table.(sql.Indexable).IndexKeyValueIter(ctx, data.columns)
	require.NoError(err)

	require.NoError(driver.Save(context.Background(), idx, iter))

	done <- struct{}{}
}

func deleteIndex(
	t testing.TB,
	e *sqle.Engine,
	data indexData,
) {
	t.Helper()
	done, err := e.Catalog.DeleteIndex("foo", data.id)
	require.NoError(t, err)
	<-done
}

func setup(t testing.TB) (*sqle.Engine, *gitbase.RepositoryPool, func()) {
	t.Helper()
	engine := sqle.New()
	require.NoError(t, fixtures.Init())
	cleanup := func() {
		require.NoError(t, fixtures.Clean())
	}

	pool := gitbase.NewRepositoryPool()
	for _, f := range fixtures.ByTag("worktree") {
		pool.AddGit(f.Worktree().Root())
	}

	engine.AddDatabase(gitbase.NewDatabase("foo"))
	engine.Catalog.RegisterFunctions(function.Functions)

	return engine, pool, cleanup
}
