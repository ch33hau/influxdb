package hh

import (
	"expvar"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/influxdb/influxdb"
	"github.com/influxdb/influxdb/meta"
	"github.com/influxdb/influxdb/models"
)

var ErrHintedHandoffDisabled = fmt.Errorf("hinted handoff disabled")

const (
	writeShardReq       = "write_shard_req"
	writeShardReqPoints = "write_shard_req_points"
	processReq          = "process_req"
	processReqFail      = "process_req_fail"
)

type Service struct {
	mu      sync.RWMutex
	wg      sync.WaitGroup
	closing chan struct{}

	processors map[uint64]*NodeProcessor

	statMap *expvar.Map
	Logger  *log.Logger
	cfg     Config

	shardWriter shardWriter
	metastore   metaStore
}

type shardWriter interface {
	WriteShard(shardID, ownerID uint64, points []models.Point) error
}

type metaStore interface {
	Node(id uint64) (ni *meta.NodeInfo, err error)
}

// NewService returns a new instance of Service.
func NewService(c Config, w shardWriter, m metaStore) *Service {
	key := strings.Join([]string{"hh", c.Dir}, ":")
	tags := map[string]string{"path": c.Dir}

	return &Service{
		cfg:         c,
		statMap:     influxdb.NewStatistics(key, "hh", tags),
		Logger:      log.New(os.Stderr, "[handoff] ", log.LstdFlags),
		shardWriter: w,
		metastore:   m,
	}
}

func (s *Service) Open() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.cfg.Enabled {
		// Allow Open to proceed, but don't do anything.
		return nil
	}
	s.Logger.Printf("Starting hinted handoff service")
	s.closing = make(chan struct{})

	// Create the root directory if it doesn't already exist.
	s.Logger.Printf("Using data dir: %v", s.cfg.Dir)
	if err := os.MkdirAll(s.cfg.Dir, 0700); err != nil {
		return fmt.Errorf("mkdir all: %s", err)
	}

	// Create a node processor for each node directory.
	files, err := ioutil.ReadDir(s.cfg.Dir)
	if err != nil {
		return err
	}

	for _, file := range files {
		nodeID, err := strconv.ParseUint(file.Name(), 10, 64)
		if err != nil {
			// Not a number? Skip it.
			continue
		}

		n := NewNodeProcessor(nodeID, s.pathforNode(nodeID), s.shardWriter, s.metastore)
		if err := n.Open(); err != nil {
			return err
		}
		s.processors[nodeID] = n
	}

	s.wg.Add(1)
	go s.purgeInactiveProcessors()

	return nil
}

func (s *Service) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, p := range s.processors {
		if err := p.Close(); err != nil {
			return err
		}
	}

	if s.closing != nil {
		close(s.closing)
	}
	s.wg.Wait()
	s.closing = nil

	return nil
}

// SetLogger sets the internal logger to the logger passed in.
func (s *Service) SetLogger(l *log.Logger) {
	s.Logger = l
}

// WriteShard queues the points write for shardID to node ownerID to handoff queue
func (s *Service) WriteShard(shardID, ownerID uint64, points []models.Point) error {
	if !s.cfg.Enabled {
		return ErrHintedHandoffDisabled
	}
	s.statMap.Add(writeShardReq, 1)
	s.statMap.Add(writeShardReqPoints, int64(len(points)))

	s.mu.RLock()
	processor, ok := s.processors[ownerID]
	s.mu.RUnlock()
	if !ok {
		if err := func() error {
			// Check again under write-lock.
			s.mu.Lock()
			defer s.mu.Unlock()

			processor, ok = s.processors[ownerID]
			if !ok {
				p := NewNodeProcessor(ownerID, s.pathforNode(ownerID), s.shardWriter, s.metastore)
				if err := processor.Open(); err != nil {
					return err
				}
				s.processors[ownerID] = p
				processor = s.processors[ownerID]
			}
			return nil
		}(); err != nil {
			return err
		}
	}

	if err := processor.WriteShard(shardID, points); err != nil {
		return err
	}

	return nil
}

// deleteInactiveQueues will cause the service to remove queues for inactive nodes.
func (s *Service) purgeInactiveProcessors() {
	defer s.wg.Done()
	ticker := time.NewTicker(time.Duration(s.cfg.PurgeInterval))
	defer ticker.Stop()

	for {
		select {
		case <-s.closing:
			return
		case <-ticker.C:
			func() {
				s.mu.Lock()
				defer s.mu.Unlock()

				for k, v := range s.processors {
					lm, err := v.LastModified()
					if err != nil {
						s.Logger.Println("failed to determine LastModified for processor %d: %s", k, err.Error())
						continue
					}

					ni, err := s.metastore.Node(k)
					if err != nil {
						s.Logger.Println("failed to determine if node %d is active: %s", k, err.Error())
						continue
					}
					if ni != nil {
						// Node is active.
						continue
					}

					if !lm.Before(time.Now().Add(-time.Duration(s.cfg.MaxAge))) {
						// Node processor contains too-young data.
						continue
					}

					if err := v.Close(); err != nil {
						s.Logger.Println("failed to close node processor %d: %s", k, err.Error())
						continue
					}
					if err := v.Purge(); err != nil {
						s.Logger.Println("failed to close node processor %d: %s", k, err.Error())
						continue
					}
					delete(s.processors, k)
				}
			}()
		}
	}
}

// pathforNode returns the directory for HH data, for the given node.
func (s *Service) pathforNode(nodeID uint64) string {
	return filepath.Join(s.cfg.Dir, fmt.Sprintf("%d", nodeID))
}
