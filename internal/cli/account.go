package cli

import (
	"fmt"
	"os"
	"syscall"

	"golang.org/x/term"
)

func cmdLogin(c *client, args []string) error {
	if len(args) == 0 {
		var status struct {
			LoggedIn   bool   `json:"loggedIn"`
			Email      string `json:"email"`
			BackendURL string `json:"backendUrl"`
		}
		if err := c.getInto("/api/auth/status", &status); err != nil {
			return err
		}
		if !status.LoggedIn {
			fmt.Println("Kirilmagan. Kirish uchun: servergo login <email>")
			return nil
		}
		fmt.Printf("Kirilgan: %s (%s)\n", status.Email, status.BackendURL)
		return nil
	}

	email := args[0]
	fmt.Fprint(os.Stderr, "Parol: ")
	pw, err := term.ReadPassword(int(syscall.Stdin))
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return fmt.Errorf("parol o'qilmadi: %w", err)
	}

	hostname, _ := os.Hostname()
	req := map[string]string{"email": email, "password": string(pw), "deviceName": hostname}

	var resp struct {
		Email string `json:"email"`
	}
	if err := c.postInto("/api/auth/login", req, &resp); err != nil {
		return err
	}
	fmt.Printf("Kirdingiz: %s\n", resp.Email)
	return nil
}

func cmdLogout(c *client, args []string) error {
	if _, err := c.post("/api/auth/logout", nil); err != nil {
		return err
	}
	fmt.Println("Chiqdingiz.")
	return nil
}
