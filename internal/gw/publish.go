package gw

import (
	"context"
	"fmt"
	"math"

	"github.com/release-engineering/exodus-rsync/internal/log"
)

type publish struct {
	client *client
	raw    struct {
		ID    string
		Env   string
		State string
		Links map[string]string
	}
}

// ItemInput is a single item accepted for publish by the AddItems method.
type ItemInput struct {
	WebURI      string `json:"web_uri"`
	ObjectKey   string `json:"object_key"`
	ContentType string `json:"content_type"`
	LinkTo      string `json:"link_to"`
}

// NewPublish creates and returns a new publish object within exodus-gw.
func (c *client) NewPublish(ctx context.Context) (Publish, error) {
	if c.dryRun {
		return &dryRunPublish{}, nil
	}

	url := "/" + c.cfg.GwEnv() + "/publish"

	out := &publish{}
	headers := map[string][]string{"X-Idempotency-Key": {}}
	if err := c.doJSONRequest(ctx, "POST", url, nil, &out.raw, headers); err != nil {
		return out, err
	}

	out.client = c

	return out, nil
}

func (c *client) GetPublish(ctx context.Context, id string) (Publish, error) {
	if c.dryRun {
		return &dryRunPublish{}, nil
	}

	url := "/" + c.cfg.GwEnv() + "/publish/" + id

	out := &publish{}
	out.client = c

	// Make up the content of the publish object as we expect
	// exodus-gw would have returned it. We do not actually know
	// whether this is valid - we'll find out later when we try to
	// use it.
	out.raw.ID = id
	out.raw.Env = c.cfg.GwEnv()
	out.raw.Links = make(map[string]string)
	out.raw.Links["self"] = url
	out.raw.Links["commit"] = url + "/commit"

	// Verify that the publish ID is valid before uploading blobs.
	empty := struct{}{}
	if err := c.doJSONRequest(ctx, "GET", url, nil, &empty, nil); err != nil {
		return nil, err
	}

	return out, nil
}

func (p *publish) ID() string {
	return p.raw.ID
}

// AddItems will add all of the specified items onto this publish.
// This may involve multiple requests to exodus-gw.
func (p *publish) AddItems(ctx context.Context, items []ItemInput) error {
	c := p.client
	url, ok := p.raw.Links["self"]
	if !ok {
		return fmt.Errorf("publish object is missing 'self' link: %+v", p.raw)
	}

	logger := log.FromContext(ctx)

	var batch []ItemInput
	batchSize := p.client.cfg.GwBatchSize()

	nextBatch := func() {
		if batchSize > len(items) {
			batchSize = len(items)
		}
		batch = items[0:batchSize]
		items = items[batchSize:]
	}

	count := 0
	empty := struct{}{}
	totalBatches := math.Ceil(float64(len(items)) / float64(batchSize))

	for nextBatch(); len(batch) > 0; nextBatch() {
		count++
		// Log the current batch number at Info to serve as a gradual progress indicator.
		logger.F("currentBatch", count, "totalBatches", totalBatches).Info("Preparing the next batch of items")

		for _, item := range batch {
			logger.F("item", item, "url", url).Debug("Adding to publish object")
		}

		headers := map[string][]string{"X-Idempotency-Key": {}}
		err := c.doJSONRequest(ctx, "PUT", url, batch, &empty, headers)
		if err != nil {
			return err
		}
	}

	return nil
}

// Commit will cause this publish object to become committed, making all of
// the included content available from the CDN.
//
// The commit operation within exodus-gw is asynchronous. This method will
// wait for the commit to complete fully and will return nil only if the
// commit has succeeded.
func (p *publish) Commit(ctx context.Context) error {
	var err error

	logger := log.FromContext(ctx)
	defer logger.F("publish", p.ID()).Trace("Committing publish").Stop(&err)

	c := p.client
	url, ok := p.raw.Links["commit"]
	if !ok {
		err = fmt.Errorf("publish not eligible for commit: %+v", p.raw)
		return err
	}

	task := task{}
	headers := map[string][]string{"X-Idempotency-Key": {}}
	if err := c.doJSONRequest(ctx, "POST", url, nil, &task.raw, headers); err != nil {
		return err
	}

	task.client = c

	err = task.Await(ctx)
	return err
}
