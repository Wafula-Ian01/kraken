package cdb

import (
	"centurion/linkgraph/graph"
	"database/sql"
	"github.com/google/uuid"
	"github.com/lib/pq"
	"golang.org/x/xerrors"
	"time"
)

var (
	//upserting link query
	UpsertLinkQuery = "INSERT INTO links(url, retrieved_at) VALUES($1, $2) ON CONFLICT (url) DO UPDATE SET retrieved_at= GREATEST(links.retrieved_at, $2) RETURNING retrieved_at, id"

	//find link query
	FindLinkQuery = "SELECT id, retrieved_at FROM links WHERE id = $1"

	//links in partition query
	LinksInPartitionQuery = "SELECT id, url, retrieved_at FROM links WHERE id>= $1 AND id< $2 AND retrieved_at< $3"

	//upserting edge query
	UpsertEdgeQuery = "INSERT INTO edges(src, dst, updated_at) VALUES($1, $2, NOW()) ON CONFLICT (src, src) DO UPDATE SET updated_at= NOW() RETURNING id, updated_at"

	//edges in partition query
	edgesInPartitionQuery = "SELECT id, src, dst, updated_at FROM edges WHERE src >= $1 AND src < $2 AND updated_at < $3"

	//removal of stale edges
	removeStaleEdgesQuery = "DELETE FROM edges WHERE src=$1 AND updated_at < $2"

	// Compile-time check for ensuring CockroachDbGraph implements Graph.
	_ graph.Graph = (*CockroachDBGraph)(nil)
)

// CockroachDBGraph implements a graph that persists its links and edges to a
// cockroachdb instance.
type CockroachDBGraph struct {
	db *sql.DB
}

// NewCockroachDbGraph returns a CockroachDbGraph instance that connects to the cockroachdb
// instance specified by dsn.
func NewCockroachDbGraph(dsn string) (*CockroachDBGraph, error) {
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, err
	}

	return &CockroachDBGraph{db: db}, nil
}

// Close terminates the connection to the backing cockroachdb instance.
func (c *CockroachDBGraph) Close() error {
	return c.db.Close()
}

func (c *CockroachDBGraph) UpsertLink(link *graph.Link) error {
	row := c.db.QueryRow(UpsertLinkQuery, link.URL, link.RetrievedAt.UTC())
	if err := row.Scan(&link.ID, &link.RetrievedAt); err != nil {
		return xerrors.Errorf("%w", err)
	}
	link.RetrievedAt = link.RetrievedAt.UTC()
	return nil
}

func (c *CockroachDBGraph) FindLink(id uuid.UUID) (*graph.Link, error) {
	row := c.db.QueryRow(FindLinkQuery, id)
	link := &graph.Link{ID: id}
	if err := row.Scan(&link.ID, &link.RetrievedAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, xerrors.Errorf("find link: %w", graph.ErrNotFound)
		}
		return nil, xerrors.Errorf("find link: %w", err)
	}
	link.RetrievedAt = link.RetrievedAt.UTC()
	return link, nil
}

func (c *CockroachDBGraph) Links(fromID, toID uuid.UUID, accessedBefore time.Time) (graph.LinkIterator, error) {
	rows, err := c.db.Query(LinksInPartitionQuery, fromID, toID, accessedBefore.UTC())
	if err != nil {
		xerrors.Errorf("links: %w")
	}
	return &linkIterator{rows: rows}, nil
}

func (c *CockroachDBGraph) UpsertEdge(edge *graph.Edge) error {
	row := c.db.QueryRow(UpsertEdgeQuery, edge.Src, edge.Dst)
	if err := row.Scan(&edge.ID, &edge.UpdatedAt); err != nil {
		if isForeignKeyViolationError(err) {
			err = graph.ErrUnknownEdgeLinks
		}
		return xerrors.Errorf("%w", err)
	}
	edge.UpdatedAt = edge.UpdatedAt.UTC()
	return nil
}

func (c *CockroachDBGraph) Edges(fromID, toID uuid.UUID, updatedBefore time.Time) (graph.EdgeIterator, error) {
	rows, err := c.db.Query(edgesInPartitionQuery, fromID, toID, updatedBefore.UTC())
	if err != nil {
		return nil, xerrors.Errorf("edges: %w", err)
	}

	return &edgeIterator{rows: rows}, nil
}

func (c *CockroachDBGraph) RemoveStaleEdges(fromID uuid.UUID, updatedBefore time.Time) error {
	_, err := c.db.Exec(removeStaleEdgesQuery, fromID, updatedBefore.UTC())
	if err != nil {
		return xerrors.Errorf("remove stale edges: %w", err)
	}

	return nil
}

func isForeignKeyViolationError(err error) bool {
	pqErr, valid := err.(*pq.Error)

	if !valid {
		return false
	}
	return pqErr.Code.Name() == "foreign_key_violation"
}
