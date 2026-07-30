package main

import (
	"context"
	"errors"
	"os"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/alicloud/datacache"
)

type fakeRemoteBaselineDataCacheClient struct {
	describe     [][]datacache.DataCache
	find         [][]datacache.DataCache
	findBucket   []string
	findPath     []string
	findTags     []map[string]string
	deleted      []string
	events       *[]string
	createCalls  int
	renewAllowed bool
	renewed      []struct {
		id            string
		retentionDays int
		token         string
	}
}

func (client *fakeRemoteBaselineDataCacheClient) Create(
	context.Context,
	datacache.CreateRequest,
) (datacache.DataCache, error) {
	client.createCalls++
	return datacache.DataCache{}, errors.New("unexpected Create call")
}

func (client *fakeRemoteBaselineDataCacheClient) Describe(
	_ context.Context,
	_ ...string,
) ([]datacache.DataCache, error) {
	if len(client.describe) == 0 {
		return nil, errors.New("unexpected Describe call")
	}
	result := client.describe[0]
	client.describe = client.describe[1:]
	return result, nil
}

func (client *fakeRemoteBaselineDataCacheClient) FindByPath(
	_ context.Context,
	bucket string,
	cachePath string,
	tags map[string]string,
) ([]datacache.DataCache, error) {
	if len(client.find) == 0 {
		return nil, errors.New("unexpected FindByPath call")
	}
	client.findBucket = append(client.findBucket, bucket)
	client.findPath = append(client.findPath, cachePath)
	client.findTags = append(client.findTags, map[string]string{
		"owner":      tags["owner"],
		"generation": tags["generation"],
	})
	if client.events != nil {
		*client.events = append(*client.events, "find-cache")
	}
	result := client.find[0]
	client.find = client.find[1:]
	return result, nil
}

func (client *fakeRemoteBaselineDataCacheClient) Renew(
	_ context.Context,
	id string,
	retentionDays int,
	token string,
) error {
	if !client.renewAllowed {
		return errors.New("unexpected Renew call")
	}
	client.renewed = append(client.renewed, struct {
		id            string
		retentionDays int
		token         string
	}{id: id, retentionDays: retentionDays, token: token})
	return nil
}

func (client *fakeRemoteBaselineDataCacheClient) Delete(
	_ context.Context,
	id string,
	_ string,
	_ string,
) error {
	client.deleted = append(client.deleted, id)
	if client.events != nil {
		*client.events = append(*client.events, "delete-cache")
	}
	return nil
}

type fakeRemoteBaselineOSSStore struct {
	deletedPrefixes    []string
	deletePrefixErrors []error
	downloads          map[string][]byte
	downloadedKeys     []string
	uploadedKeys       []string
	uploadErrors       []error
	events             *[]string
}

func (store *fakeRemoteBaselineOSSStore) Upload(_ context.Context, _ string, key string) error {
	store.uploadedKeys = append(store.uploadedKeys, key)
	if len(store.uploadErrors) == 0 {
		return nil
	}
	err := store.uploadErrors[0]
	store.uploadErrors = store.uploadErrors[1:]
	return err
}

func (store *fakeRemoteBaselineOSSStore) Download(_ context.Context, key string, destination string) error {
	store.downloadedKeys = append(store.downloadedKeys, key)
	data, exists := store.downloads[key]
	if !exists {
		return errors.New("unexpected Download call")
	}
	return os.WriteFile(destination, data, 0o600)
}

func (*fakeRemoteBaselineOSSStore) EnsurePrefix(context.Context, string) error {
	return nil
}

func (store *fakeRemoteBaselineOSSStore) DeletePrefix(_ context.Context, prefix string) error {
	store.deletedPrefixes = append(store.deletedPrefixes, prefix)
	if store.events != nil {
		*store.events = append(*store.events, "delete-prefix")
	}
	if len(store.deletePrefixErrors) > 0 {
		err := store.deletePrefixErrors[0]
		store.deletePrefixErrors = store.deletePrefixErrors[1:]
		return err
	}
	return nil
}
