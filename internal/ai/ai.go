package ai

import (
	"fmt"
	"os/exec"
	"strings"

	"github.com/gausejakub/vimail/internal/config"
)

const systemPrompt = `You are helping compose an email.
To: %s
Subject: %s

If the body below contains instructions (like "write a polite decline"),
follow them and write the email. If it contains an existing email draft,
improve its grammar, tone, and clarity. If it contains quoted text (lines
starting with >), write a reply to it.

Output ONLY the email body text, no headers, no markdown formatting.`

func Generate(cfg config.AIConfig, agentName, to, subject, body string) (string, error) {
	agent, err := resolveAgent(cfg, agentName)
	if err != nil {
		return "", err
	}

	binPath, err := exec.LookPath(agent.Cmd)
	if err != nil {
		return "", fmt.Errorf("%s not found in PATH", agent.Cmd)
	}

	prompt := fmt.Sprintf(systemPrompt, to, subject) + "\n\n" + body

	args := make([]string, len(agent.Args))
	for i, a := range agent.Args {
		args[i] = strings.ReplaceAll(a, "{prompt}", prompt)
	}

	cmd := exec.Command(binPath, args...)
	out, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return "", fmt.Errorf("%s failed: %s", agent.Name, string(exitErr.Stderr))
		}
		return "", fmt.Errorf("%s failed: %w", agent.Name, err)
	}

	return strings.TrimSpace(string(out)), nil
}

func resolveAgent(cfg config.AIConfig, name string) (config.AIAgentConfig, error) {
	if name == "" {
		name = cfg.Default
	}
	for _, a := range cfg.Agents {
		if a.Name == name {
			return a, nil
		}
	}
	if name != "" {
		return config.AIAgentConfig{}, fmt.Errorf("unknown ai agent %q", name)
	}
	return config.AIAgentConfig{}, fmt.Errorf("no ai agents configured")
}

func AgentNames(cfg config.AIConfig) []string {
	names := make([]string, len(cfg.Agents))
	for i, a := range cfg.Agents {
		names[i] = a.Name
	}
	return names
}
