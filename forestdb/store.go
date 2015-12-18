//  Copyright (c) 2014 Couchbase, Inc.
//  Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file
//  except in compliance with the License. You may obtain a copy of the License at
//    http://www.apache.org/licenses/LICENSE-2.0
//  Unless required by applicable law or agreed to in writing, software distributed under the
//  License is distributed on an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND,
//  either express or implied. See the License for the specific language governing permissions
//  and limitations under the License.

package forestdb

import (
	"fmt"
	"sync"

	"bytes"
	"encoding/binary"
	"github.com/blevesearch/bleve/index/store"
	"github.com/blevesearch/bleve/registry"
	"github.com/couchbase/goforestdb"
)

const Name = "forestdb"
const DefaultConcurrent = 10

type Store struct {
	m      sync.RWMutex
	path   string
	kvpool *forestdb.KVPool
	mo     store.MergeOperator
}

func New(mo store.MergeOperator, config map[string]interface{}) (store.KVStore, error) {
	fmt.Println("creating a new forestdb data pool")

	path, ok := config["path"].(string)
	if !ok {
		return nil, fmt.Errorf("must specify path")
	}

	forestDBDefaultConfig := forestdb.DefaultConfig()
	forestDBDefaultConfig.SetCompactionMode(forestdb.COMPACT_AUTO)
	forestDBDefaultConfig.SetMultiKVInstances(false)
	forestDBConfig, err := applyConfig(forestDBDefaultConfig, config)
	if err != nil {
		return nil, err
	}

	kvconfig := forestdb.DefaultKVStoreConfig()
	if cim, ok := config["create_if_missing"].(bool); ok && cim {
		kvconfig.SetCreateIfMissing(true)
	}

	numConcurrent := DefaultConcurrent
	if nc, ok := config["num_concurrent"].(float64); ok {
		numConcurrent = int(nc)
	}

	kvpool, err := forestdb.NewKVPool(path, forestDBConfig, "default", kvconfig, numConcurrent)
	if err != nil {
		return nil, err
	}

	rv := Store{
		path:   path,
		mo:     mo,
		kvpool: kvpool,
	}

	return &rv, nil
}

func (s *Store) Close() error {
	return s.kvpool.Close()
}

func (s *Store) Reader() (store.KVReader, error) {
	kvstore, err := s.kvpool.Get()
	if err != nil {
		return nil, err
	}
	snapshot, err := kvstore.SnapshotOpen(forestdb.SnapshotInmem)
	if err != nil {
		return nil, err
	}
	return &Reader{
		store:    s,
		kvstore:  kvstore,
		snapshot: snapshot,
	}, nil
}

func (s *Store) Writer() (store.KVWriter, error) {
	kvstore, err := s.kvpool.Get()
	if err != nil {
		return nil, err
	}
	return &Writer{
		store:   s,
		kvstore: kvstore,
	}, nil
}

func (s *Store) Rollback(PartId string, seq uint64) error {
	kvstore, err := s.kvpool.Get()
	if err != nil {
		return err
	}
	snInfo, err := kvstore.File().GetAllSnapMarkers()
	if err != nil {
		return err
	}
	for _, s1 := range snInfo.SnapInfoList() {
		if s1.GetNumKvsMarkers() != 1 {
			err = fmt.Errorf("Invalid kvstore with forestdb")
			break
		}
		c := s1.GetKvsCommitMarkers()[0]
		mk := c.GetSeqNum()
		dbSnapshot, err := kvstore.SnapshotOpen(mk)
		if err != nil {
			continue
		}
		res, err := dbSnapshot.GetKV([]byte(PartId))
		if err != nil {
			continue
		}
		var data uint64
		buf := bytes.NewReader(res)
		err = binary.Read(buf, binary.BigEndian, &data)
		if err != nil {
			return err
		}
		if data < seq {
			kvstore.Rollback(forestdb.SeqNum(data))
			return nil
		}
		dbSnapshot.Close()
	}
	//s.kvpool.Close()
	return fmt.Errorf("Full rollback is required")
}

func init() {
	registry.RegisterKVStore(Name, New)
}
