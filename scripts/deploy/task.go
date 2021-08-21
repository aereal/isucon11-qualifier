package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	osexec "os/exec"
	"strings"

	"github.com/k1LoW/exec"
	"github.com/slack-go/slack"
	"golang.org/x/sync/errgroup"
)

type task interface {
	Name() string
	Run(ctx context.Context) error
}

func newSimpleCommandTask(name string, args ...string) *simpleCommandTask {
	return &simpleCommandTask{
		path: name,
		args: args,
	}
}

type simpleCommandTask struct {
	path string
	args []string
	dir  string
}

var _ task = &simpleCommandTask{}

func (t *simpleCommandTask) command(ctx context.Context) *osexec.Cmd {
	cmd := (&exec.Exec{
		KillAfterCancel: -1,
		Signal:          os.Interrupt,
	}).CommandContext(ctx, t.path, t.args...)
	if t.dir != "" {
		cmd.Dir = t.dir
	}
	return cmd
}

func (t *simpleCommandTask) Name() string {
	return t.command(context.Background()).String()
}

func (t *simpleCommandTask) Run(ctx context.Context) error {
	cmd := t.command(ctx)
	out, err := cmd.Output()
	if err != nil {
		return err
	}
	log.Printf("command=%s: output=%s", cmd, out)
	return err
}

func newParallelTask(tasks ...task) *parallelTask {
	return &parallelTask{tasks: tasks}
}

type parallelTask struct {
	tasks []task
}

var _ task = &parallelTask{}

func (t *parallelTask) Add(child task) {
	t.tasks = append(t.tasks, child)
}

func (t *parallelTask) Name() string {
	names := make([]string, len(t.tasks))
	for i, u := range t.tasks {
		names[i] = u.Name()
	}
	return fmt.Sprintf("ParallelTask(%s)", strings.Join(names, ", "))
}

func (t *parallelTask) Run(ctx context.Context) error {
	eg, ctx := errgroup.WithContext(ctx)
	for _, child := range t.tasks {
		c := child
		eg.Go(func() error {
			return c.Run(ctx)
		})
	}
	return eg.Wait()
}

type loggingTask func(ctx context.Context)

var _ task = loggingTask(nil)

func (t loggingTask) Name() string {
	return "logging"
}

func (t loggingTask) Run(ctx context.Context) error {
	t(ctx)
	return nil
}

type seqTasks []task

var _ task = &seqTasks{}

func (xs *seqTasks) Add(t task) {
	*xs = append(*xs, t)
}

func (xs *seqTasks) Name() string {
	return fmt.Sprintf("SequentialTasks(%d)", len(*xs))
}

func (xs *seqTasks) Run(ctx context.Context) error {
	for _, child := range *xs {
		c := child
		log.Printf("start task=%s", c.Name())
		if err := c.Run(ctx); err != nil {
			log.Printf("error task=%s error=%s", c.Name(), err)
			return err
		}
		log.Printf("done task=%s", c.Name())
	}
	return nil
}

func newPostSlackTask(user, text string, blocks *slack.Blocks) *postSlack {
	msg := &slack.WebhookMessage{
		Channel:   "#general",
		Text:      text,
		IconEmoji: ":rocket:",
		Username:  fmt.Sprintf("deploy(%s)", user),
		Blocks:    blocks,
	}
	return &postSlack{
		Message: msg,
	}
}

type postSlack struct {
	Message *slack.WebhookMessage
}

var _ task = &postSlack{}

func (p *postSlack) Name() string {
	return "postSlack"
}

func (p *postSlack) Run(ctx context.Context) error {
	return slack.PostWebhookContext(ctx, slackWebhookURL, p.Message)
}

type recordNewRelicDeploymentTask struct {
	AppID   string        `json:"-"`
	APIKey  string        `json:"-"`
	Payload *nrDeployment `json:"deployment"`
}

type nrDeployment struct {
	Revision    string `json:"revision"`
	Changelog   string `json:"changelog"`
	Description string `json:"description"`
	User        string `json:"user"`
}

var _ task = &recordNewRelicDeploymentTask{}

func (t *recordNewRelicDeploymentTask) Name() string {
	return "RecordNewRelicDeployment"
}

func (t *recordNewRelicDeploymentTask) Run(ctx context.Context) error {
	if t.APIKey == "TODO" || t.AppID == "TODO" {
		log.Printf("skip record new relic deployment; APIKey and AppID must be configured")
		return nil
	}
	payload, err := json.Marshal(t)
	if err != nil {
		return err
	}
	reqURL := fmt.Sprintf(newrelicRecordDeploymentURLTemplate, t.AppID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("x-api-key", t.APIKey)
	req.Header.Set("content-type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if !(resp.StatusCode >= 200 && resp.StatusCode < 300) {
		b, _ := ioutil.ReadAll(resp.Body)
		return fmt.Errorf("failed to request New Relic deployments: status=%d; body=%q", resp.StatusCode, string(b))
	}
	return nil
}
