package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/url"
	"os"
	"os/exec"
	"path"
	"strings"

	"github.com/slack-go/slack"
)

var (
	slackWebhookURL                     = "https://hooks.slack.com/services/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"
	newrelicRecordDeploymentURLTemplate = "https://api.newrelic.com/v2/applications/%s/deployments.json"
	newrelicAppID                       = "TODO"
	newrelicAPIKey                      = "459379558858587ddcde210a48cb54c34f39NRAL"
	osUserMapping                       = map[string]*userInfo{
		"aereal":             {githubID: 87649, slackID: "U0289QVBV8S"},
		"kenta.sato":         {githubID: 374550, slackID: "U028G2PV9FW"},
		"nishimura.tomohiro": {githubID: 9955, slackID: "U028GC0FRAQ"},
	}
)

func main() {
	if err := run(); err != nil {
		fmt.Printf("%+v\n", err)
		os.Exit(1)
	}
}

func getDescriptiveRevision() (string, error) {
	cmd := exec.Command("git", "describe", "--dirty", "--always", "--abbrev=0")
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func getCurrentBranch() (string, error) {
	cmd := exec.Command("git", "rev-parse", "--symbolic-full-name", "HEAD")
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func getCommitMessage() (string, error) {
	cmd := exec.Command("git", "show", "-s", "--oneline", "HEAD")
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func getSha() (string, error) {
	cmd := exec.Command("git", "rev-parse", "HEAD")
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func run() error {
	targets, err := getTargetServersFrom(os.Args)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	revision, err := getDescriptiveRevision()
	if err != nil {
		return err
	}
	revSha, err := getSha()
	if err != nil {
		return err
	}
	branch, err := getCurrentBranch()
	if err != nil {
		return err
	}
	commitMsg, err := getCommitMessage()
	if err != nil {
		return err
	}
	fullName, err := parseGitHubRepoFromRemoteURL("origin")
	if err != nil {
		return err
	}
	user := os.Getenv("USER")
	workflow := &seqTasks{
		newBuildTask(),
		newDeploySlackNotification("Start deploy", user, fullName, revision, revSha, branch, targets),
	}
	deployTasks := &parallelTask{}
	for _, target := range targets {
		deployTasks.Add(newDeployTask(target))
	}
	workflow.Add(deployTasks)
	workflow.Add(newDeploySlackNotification("Finished deploy", user, fullName, revision, revSha, branch, targets))
	workflow.Add(&recordNewRelicDeploymentTask{
		AppID:  newrelicAppID,
		APIKey: newrelicAPIKey,
		Payload: &nrDeployment{
			Revision:    revision,
			Changelog:   fmt.Sprintf("%s deployed %s: %s", user, revision, commitMsg),
			Description: fmt.Sprintf("%s deployed %s: %s", user, revision, commitMsg),
			User:        user,
		},
	})
	if err := workflow.Run(ctx); err != nil {
		return err
	}
	return nil
}

func newBuildTask() task {
	t := newSimpleCommandTask("make", "app_linux_amd64")
	return t
}

func newDeployTask(target string) task {
	return &seqTasks{
		loggingTask(func(_ context.Context) {
			log.Printf("start deploy to %s", target)
		}),
		// 初期データ消えるので --delete 付けていない
		newSimpleCommandTask("rsync", rsyncArgs("./sql/", fmt.Sprintf("%s:webapp/sql/", target))...),
		// go.mod消えるので --delete 付けていない
		newSimpleCommandTask("rsync", rsyncArgs("./go.mod", fmt.Sprintf("%s:webapp/go/", target))...),
		newSimpleCommandTask("rsync", rsyncArgs("./go.sum", fmt.Sprintf("%s:webapp/go/", target))...),
		newSimpleCommandTask("rsync", rsyncArgs("./go/", fmt.Sprintf("%s:webapp/go/", target))...),
		newSimpleCommandTask("ssh", "-n", target, "sudo", "-u", "isucon", "-i", "mv", "webapp/go/app_linux_amd64", "webapp/go/isucondition"),
		// 再起動 & ステータス表示
		newSimpleCommandTask("ssh", "-n", target, "sudo", "systemctl", "restart", "isucondition.go.service"),
		newSimpleCommandTask("ssh", "-n", target, "sudo", "systemctl", "status", "isucondition.go.service"),
		loggingTask(func(_ context.Context) {
			log.Printf("DONE: deploy to %s", target)
		}),
	}
}

func rsyncArgs(from, to string) []string {
	return []string{"-avzL", "--exclude", ".git*", "-e", "ssh", "--rsync-path", "sudo -u isucon -i rsync", from, to}
}

func newDeploySlackNotification(title, osUser, repoFullName, version, revision, branch string, targetHosts []string) *postSlack {
	targetsLine := strings.Join(targetHosts, ", ")
	blocks := &slack.Blocks{}
	blocks.BlockSet = []slack.Block{
		slack.NewHeaderBlock(slack.NewTextBlockObject(slack.PlainTextType, title, false, false)),
		slack.NewSectionBlock(
			nil,
			[]*slack.TextBlockObject{
				markdownTextBlockObject(fmt.Sprintf("revision: <https://github.com/%s/commit/%s|`%s`>", repoFullName, revision, version)),
				markdownTextBlockObject(fmt.Sprintf("branch: `%s`", branch)),
			},
			nil),
		slack.NewContextBlock(
			"",
			slack.NewImageBlockElement(avatarURL(osUser), osUser),
			markdownTextBlockObject(fmt.Sprintf("Deployed by <@%s>", osUserMapping[osUser].slackID))),
	}
	return newPostSlackTask(osUser, fmt.Sprintf("start deploy revision=%s (branch=%s) to %s by %s", revision, branch, targetsLine, osUser), blocks)
}

func markdownTextBlockObject(text string) *slack.TextBlockObject {
	return slack.NewTextBlockObject(slack.MarkdownType, text, false, false)
}

func getTargetServersFrom(argv []string) ([]string, error) {
	fs := flag.NewFlagSet(path.Base(argv[0]), flag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), "Usage: %s SERVER1 [, SERVER2, ...]\n", fs.Name())
		fs.PrintDefaults()
	}
	err := fs.Parse(argv[1:])
	if err == flag.ErrHelp {
		return nil, flag.ErrHelp
	}
	if len(fs.Args()) == 0 {
		fs.Usage()
		return nil, errors.New("servers required")
	}
	return fs.Args(), nil
}

type userInfo struct {
	githubID int
	slackID  string
}

func avatarURL(osUser string) string {
	um := osUserMapping[osUser]
	return fmt.Sprintf("https://avatars.githubusercontent.com/u/%d", um.githubID)
}

func parseGitHubRepoFromRemoteURL(remote string) (string, error) {
	cmd := exec.Command("git", "remote", "get-url", remote)
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	parsed, err := url.Parse(strings.TrimSpace(string(out)))
	if err != nil {
		return "", err
	}
	return strings.TrimSuffix(parsed.Path[1:] /* remove slash prefix */, ".git"), nil
}
