package memory

import (
	"centurion/linkgraph/graph"
	"github.com/google/uuid"
	"golang.org/x/xerrors"
	"sync"
	"time"
)

// compile-time check for In-memory graph
//var _ graph.Graph = (*InMemoryGraph)(nil)

type edgeList []uuid.UUID

// InMemoryGraph implements an In-Memory link graph which can be accessed concurrently
type InMemoryGraph struct {
	mu sync.RWMutex //mutex for managing concurrent access

	links map[uuid.UUID]*graph.Link
	edges map[uuid.UUID]*graph.Edge

	linkURLIndex map[string]*graph.Link
	linkEdgeMap  map[uuid.UUID]edgeList
}

// NewInMemoryGraph make new In-Memory graph
func NewInMemoryGraph() *InMemoryGraph {
	return &InMemoryGraph{
		links:        make(map[uuid.UUID]*graph.Link),
		edges:        make(map[uuid.UUID]*graph.Edge),
		linkURLIndex: make(map[string]*graph.Link),
		linkEdgeMap:  make(map[uuid.UUID]edgeList),
	}
}

// Upsertlink insert new link or update an existing one in the link graph
func (s *InMemoryGraph) Upsertlink(link *graph.Link) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	//check if link with same URL exists if not assign ID and timestamp and save otherwise update timestamp
	if existing := s.linkURLIndex[link.URL]; existing != nil {
		link.ID = existing.ID
		origTs := existing.RetrievedAt
		*existing = *link
		if origTs.After(existing.RetrievedAt) {
			existing.RetrievedAt = origTs
		}
		return nil
	}
	//inserting a new link to the graph
	for {
		link.ID = uuid.New()
		if s.links[link.ID] == nil {
			break
		}
	}
	lcopy := new(graph.Link)
	*lcopy = *link
	s.linkURLIndex[lcopy.URL] = lcopy
	s.links[lcopy.ID] = lcopy
	return nil
}

// FindLink looks up a link by it's ID
func (s *InMemoryGraph) FindLink(id uuid.UUID) (*graph.Link, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	link := s.links[id]
	if s.links[id] == nil {
		return nil, xerrors.Errorf("link does not exist: %w", graph.ErrNotFound)
	}
	lCopy := new(graph.Link)
	*lCopy = *link

	return lCopy, nil
}

// Links returns an iterator for the set of links whose IDs belong to the
// [fromID, toID) range and were retrieved before the provided timestamp.
func (s *InMemoryGraph) Links(fromID, toID uuid.UUID, retrievedBefore time.Time) (graph.LinkIterator, error) {
	from, to := fromID.String(), toID.String()

	s.mu.RLock()
	var list []*graph.Link
	for linkID, link := range s.links {
		if id := linkID.String(); id >= from && id < to && link.RetrievedAt.Before(retrievedBefore) {
			list = append(list, link)
		}
	}
	s.mu.RUnlock()
	return &linkIterator{s: s, links: list}, nil
}

// UpsertEdge insert an edge to the graph if edge doesn't exist create a new one
func (s *InMemoryGraph) UpsertEdge(edge *graph.Edge) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, srcExists := s.links[edge.Src]
	_, dstExists := s.links[edge.Dst]

	if !srcExists || !dstExists {
		return xerrors.Errorf("upsert edge: %w", graph.ErrUnknownEdgeLinks)
	}

	//scan the edge list from the source
	for _, edgeID := range s.linkEdgeMap[edge.Src] {
		existingEdge := s.edges[edgeID]
		if existingEdge.Src == edge.Src && existingEdge.Dst == edge.Dst {
			existingEdge.UpdatedAt = time.Now()
			*edge = *existingEdge
			return nil
		}
	}

	//insert a new edge to the graph
	for {
		edge.ID = uuid.New()
		if s.edges[edge.ID] == nil {
			break
		}
	}

	edge.UpdatedAt = time.Now()
	eCopy := new(graph.Edge)
	*eCopy = *edge
	s.edges[eCopy.ID] = eCopy

	//append the edge id to the list of edges originating from the edge's source links
	s.linkEdgeMap[edge.Src] = append(s.linkEdgeMap[edge.Src], eCopy.ID)
	return nil
}

func (s *InMemoryGraph) Edges(fromID, toID uuid.UUID, updatedBefore time.Time) (graph.EdgeIterator, error) {
	from, to := fromID.String(), toID.String()
	s.mu.RLock()
	var list []*graph.Edge
	for linkID := range s.links {
		if id := linkID.String(); id < from || id > to {
			continue
		}

		for _, edgeID := range s.linkEdgeMap[linkID] {
			if edge := s.edges[edgeID]; edge != nil && edge.UpdatedAt.Before(updatedBefore) {
				list = append(list, edge)
			}
		}
	}
	s.mu.RUnlock()
	return &edgeIterator{s: s, edges: list}, nil
}

func (s *InMemoryGraph) RemoveStaleEdges(fromID uuid.UUID, updatedBefore time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	var newEdgeList edgeList
	for _, edgeID := range s.linkEdgeMap[fromID] {
		edge := s.edges[edgeID]
		if edge.UpdatedAt.Before(updatedBefore) {
			delete(s.edges, edgeID)
			continue
		}
		newEdgeList = append(newEdgeList, edgeID)
	}
	s.linkEdgeMap[fromID] = newEdgeList
	return nil
}
