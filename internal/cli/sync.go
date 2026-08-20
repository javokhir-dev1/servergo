package cli

import (
	"errors"
	"fmt"
)

type syncResult struct {
	Apps     int `json:"apps"`
	Projects int `json:"projects"`
	Domains  int `json:"domains"`
}

func (r syncResult) String() string {
	return fmt.Sprintf("%d ilova, %d loyiha, %d domen sertifikati", r.Apps, r.Projects, r.Domains)
}

func cmdSync(c *client, args []string) error {
	mode := ""
	if len(args) > 0 {
		mode = args[0]
	}

	switch mode {
	case "", "all":
		fmt.Println("Push...")
		push, err := syncOne(c, "/api/sync/push")
		if err != nil {
			return err
		}
		fmt.Println("  " + push.String())
		fmt.Println("Pull...")
		pull, err := syncOne(c, "/api/sync/pull")
		if err != nil {
			return err
		}
		fmt.Println("  " + pull.String())
		return nil
	case "push":
		res, err := syncOne(c, "/api/sync/push")
		if err != nil {
			return err
		}
		fmt.Println("Push: " + res.String())
		return nil
	case "pull":
		res, err := syncOne(c, "/api/sync/pull")
		if err != nil {
			return err
		}
		fmt.Println("Pull: " + res.String())
		return nil
	default:
		return errors.New("noma'lum: sync [push|pull]")
	}
}

func syncOne(c *client, path string) (syncResult, error) {
	var res syncResult
	err := c.postInto(path, nil, &res)
	return res, err
}
