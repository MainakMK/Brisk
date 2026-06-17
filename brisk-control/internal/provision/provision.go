// Package provision onboards an edge server over SSH: install the control
// plane's key, copy the agent binary, write its config (control_plane_url +
// token), and run the idempotent bootstrap. The operator's SSH password is used
// once for the first login and never stored — the control plane installs its own
// keypair so all later operations use key auth.
package provision

import (
	"bufio"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	_ "embed"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"time"

	"brisk-control/internal/store"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
	"gopkg.in/yaml.v3"
)

// agentBinary is the linux brisk-agent binary shipped to edges. It is staged at
// build time (see README) and embedded so the control image is self-contained.
//
//go:embed assets/brisk-agent
var agentBinary []byte

const (
	agentRemotePath  = "/usr/local/bin/brisk-agent"
	agentConfigPath  = "/etc/brisk/agent.yaml"
	dialTimeout      = 20 * time.Second
	bootstrapTimeout = 8 * time.Minute
)

// SSHCreds are the one-time credentials for the first login.
type SSHCreds struct {
	User       string
	Port       int
	Password   string // optional (used once, never stored)
	PrivateKey string // optional PEM (used once, never stored)
}

// Provisioner onboards servers over SSH.
type Provisioner struct {
	store           *store.Store
	controlPlaneURL string
	natsURL         string
}

// New builds a Provisioner. controlPlaneURL + natsURL are written into each
// agent's config so the edge can reach the control plane and the purge channel.
func New(st *store.Store, controlPlaneURL, natsURL string) *Provisioner {
	return &Provisioner{store: st, controlPlaneURL: controlPlaneURL, natsURL: natsURL}
}

// Provision runs the full onboarding flow for srv, logging each step.
func (p *Provisioner) Provision(ctx context.Context, srv store.Server, creds SSHCreds, agentToken string) error {
	logf := func(level, format string, args ...any) {
		msg := fmt.Sprintf(format, args...)
		_ = p.store.AddProvisionLog(ctx, srv.ID, level, msg)
	}

	cpAuthorizedKey, cpSigner, err := p.ensureCPIdentity(ctx)
	if err != nil {
		logf("error", "control-plane key: %v", err)
		return err
	}

	client, capturedHostKey, err := p.dial(srv, creds, cpSigner)
	if err != nil {
		logf("error", "ssh dial failed: %v", err)
		return err
	}
	defer client.Close()
	logf("info", "connected to %s:%d as %s", srv.IP, effPort(creds.Port), creds.User)

	// TOFU: persist the host key on first connect.
	if capturedHostKey != "" {
		if err := p.store.SetServerHostKey(ctx, srv.ID, capturedHostKey); err != nil {
			logf("error", "store host key: %v", err)
			return err
		}
		logf("info", "pinned SSH host key (TOFU)")
	}

	// 1) install the control plane's public key so future ops use key auth.
	installKey := fmt.Sprintf(
		"mkdir -p ~/.ssh && chmod 700 ~/.ssh && touch ~/.ssh/authorized_keys && chmod 600 ~/.ssh/authorized_keys && "+
			"grep -qxF '%s' ~/.ssh/authorized_keys || echo '%s' >> ~/.ssh/authorized_keys",
		cpAuthorizedKey, cpAuthorizedKey)
	if err := run(client, installKey); err != nil {
		logf("error", "install control-plane key: %v", err)
		return err
	}
	logf("info", "installed control-plane SSH key")

	// 2) stop the agent service (if any) so the binary can be replaced (ETXTBSY).
	_ = run(client, "systemctl stop brisk-agent 2>/dev/null || true")

	// 3) copy the agent binary via SFTP.
	if err := p.copyAgent(client); err != nil {
		logf("error", "copy agent binary: %v", err)
		return err
	}
	logf("info", "copied brisk-agent (%d bytes) to %s", len(agentBinary), agentRemotePath)

	// 4) merge control_plane_url + token into agent.yaml (preserving zones).
	if err := p.writeAgentConfig(client, srv, agentToken); err != nil {
		logf("error", "write agent.yaml: %v", err)
		return err
	}
	logf("info", "wrote %s (control_plane_url + token; zones preserved)", agentConfigPath)

	// 5) idempotent bootstrap (re-safe even though the box was provisioned in Phase 1).
	logf("info", "running brisk-agent --bootstrap ...")
	bctx, cancel := context.WithTimeout(ctx, bootstrapTimeout)
	defer cancel()
	if err := runStream(bctx, client, agentRemotePath+" --bootstrap --config "+agentConfigPath, logf); err != nil {
		logf("error", "bootstrap failed: %v", err)
		return err
	}
	logf("info", "bootstrap complete; agent will heartbeat and flip status to online")
	return nil
}

// UpdateToken rewrites the agent's token over key auth and restarts it — used by
// token rotation, avoiding a full re-bootstrap. Requires the control-plane key to
// already be installed (i.e. the server was provisioned at least once).
func (p *Provisioner) UpdateToken(ctx context.Context, srv store.Server, newToken string) error {
	_, cpSigner, err := p.ensureCPIdentity(ctx)
	if err != nil {
		return err
	}
	user := "root"
	if srv.SSHUser != nil && *srv.SSHUser != "" {
		user = *srv.SSHUser
	}
	client, captured, err := p.dial(srv, SSHCreds{User: user, Port: int(srv.SSHPort)}, cpSigner)
	if err != nil {
		return err
	}
	defer client.Close()
	if captured != "" {
		_ = p.store.SetServerHostKey(ctx, srv.ID, captured)
	}
	if err := p.writeAgentConfig(client, srv, newToken); err != nil {
		return err
	}
	_ = p.store.AddProvisionLog(ctx, srv.ID, "info", "rotated agent token; restarting agent")
	return run(client, "systemctl restart brisk-agent 2>/dev/null || true")
}

// ensureCPIdentity returns the control plane's authorized_keys line + a signer,
// generating + persisting an ed25519 keypair on first use.
func (p *Provisioner) ensureCPIdentity(ctx context.Context) (string, ssh.Signer, error) {
	priv, pub, err := p.store.GetCPIdentity(ctx)
	if err != nil {
		return "", nil, err
	}
	if priv == "" {
		pubKey, privKey, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			return "", nil, err
		}
		blk, err := ssh.MarshalPrivateKey(privKey, "brisk-control")
		if err != nil {
			return "", nil, err
		}
		sshPub, err := ssh.NewPublicKey(pubKey)
		if err != nil {
			return "", nil, err
		}
		priv = string(pem.EncodeToMemory(blk))
		pub = strings.TrimSpace(string(ssh.MarshalAuthorizedKey(sshPub)))
		if err := p.store.SaveCPIdentity(ctx, priv, pub); err != nil {
			return "", nil, err
		}
	}
	signer, err := ssh.ParsePrivateKey([]byte(priv))
	if err != nil {
		return "", nil, err
	}
	return pub, signer, nil
}

// dial connects with TOFU host-key verification, returning the client and (on
// first connect) the captured host key to persist.
func (p *Provisioner) dial(srv store.Server, creds SSHCreds, cpSigner ssh.Signer) (*ssh.Client, string, error) {
	var captured string
	pinned := ""
	if srv.HostKey != nil {
		pinned = strings.TrimSpace(*srv.HostKey)
	}
	hkcb := func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		line := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(key)))
		if pinned != "" {
			if line != pinned {
				return fmt.Errorf("host key mismatch for %s (possible MITM)", hostname)
			}
			return nil
		}
		captured = line // trust on first use
		return nil
	}

	var methods []ssh.AuthMethod
	if creds.Password != "" {
		methods = append(methods, ssh.Password(creds.Password))
	}
	if creds.PrivateKey != "" {
		if s, err := ssh.ParsePrivateKey([]byte(creds.PrivateKey)); err == nil {
			methods = append(methods, ssh.PublicKeys(s))
		}
	}
	if cpSigner != nil {
		methods = append(methods, ssh.PublicKeys(cpSigner)) // works after first provision
	}

	cfg := &ssh.ClientConfig{
		User:            creds.User,
		Auth:            methods,
		HostKeyCallback: hkcb,
		Timeout:         dialTimeout,
	}
	addr := fmt.Sprintf("%s:%d", srv.IP, effPort(creds.Port))
	c, err := ssh.Dial("tcp", addr, cfg)
	if err != nil {
		return nil, "", err
	}
	return c, captured, nil
}

// copyAgent SFTPs the embedded agent binary to the remote path (0755).
func (p *Provisioner) copyAgent(c *ssh.Client) error {
	sc, err := sftp.NewClient(c)
	if err != nil {
		return err
	}
	defer sc.Close()
	if err := sc.MkdirAll("/usr/local/bin"); err != nil {
		return err
	}
	f, err := sc.Create(agentRemotePath)
	if err != nil {
		return err
	}
	if _, err := f.Write(agentBinary); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return sc.Chmod(agentRemotePath, 0o755)
}

// writeAgentConfig merges control_plane_url + agent_token + edge_id into the
// existing agent.yaml (preserving zones / TLS settings), or creates a minimal one.
func (p *Provisioner) writeAgentConfig(c *ssh.Client, srv store.Server, agentToken string) error {
	sc, err := sftp.NewClient(c)
	if err != nil {
		return err
	}
	defer sc.Close()

	m := map[string]any{}
	if rf, err := sc.Open(agentConfigPath); err == nil {
		data, _ := io.ReadAll(rf)
		rf.Close()
		_ = yaml.Unmarshal(data, &m) // best-effort; preserves existing keys incl. zones
	}
	if _, ok := m["mode"]; !ok {
		m["mode"] = "production"
	}
	m["edge_id"] = srv.EdgeID
	m["control_plane_url"] = p.controlPlaneURL
	m["agent_token"] = agentToken
	if p.natsURL != "" {
		m["nats_url"] = p.natsURL
	}

	out, err := yaml.Marshal(m)
	if err != nil {
		return err
	}
	if err := sc.MkdirAll("/etc/brisk"); err != nil {
		return err
	}
	f, err := sc.Create(agentConfigPath)
	if err != nil {
		return err
	}
	if _, err := f.Write(out); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return sc.Chmod(agentConfigPath, 0o600)
}

// --- ssh exec helpers ---

func run(c *ssh.Client, cmd string) error {
	sess, err := c.NewSession()
	if err != nil {
		return err
	}
	defer sess.Close()
	if out, err := sess.CombinedOutput(cmd); err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// runStream runs cmd and streams combined output to logf line by line.
func runStream(ctx context.Context, c *ssh.Client, cmd string, logf func(level, format string, args ...any)) error {
	sess, err := c.NewSession()
	if err != nil {
		return err
	}
	defer sess.Close()

	stdout, err := sess.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := sess.StderrPipe()
	if err != nil {
		return err
	}
	if err := sess.Start(cmd); err != nil {
		return err
	}

	var wg sync.WaitGroup
	stream := func(sc *bufio.Scanner) {
		defer wg.Done()
		sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for sc.Scan() {
			logf("info", "%s", sc.Text())
		}
	}
	wg.Add(2)
	go stream(bufio.NewScanner(stdout))
	go stream(bufio.NewScanner(stderr))

	done := make(chan error, 1)
	go func() { wg.Wait(); done <- sess.Wait() }()
	select {
	case <-ctx.Done():
		_ = sess.Signal(ssh.SIGKILL)
		return ctx.Err()
	case err := <-done:
		return err
	}
}

func effPort(p int) int {
	if p == 0 {
		return 22
	}
	return p
}
