package cli

// node_commands.go — 'toris node add' and 'toris node remove' for v0.4.0.
//
// These commands allow operators to add or remove nodes from the cluster
// registry at runtime without restarting the daemon.
//
// 'toris node add' inserts a node into toris_control.nodes.
// The node watcher loop in the daemon picks it up within watchInterval.
//
// 'toris node remove' marks a node as 'removed' in the registry.
// It refuses to remove the active primary unless --force is passed.

import (
	"context"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/tobibamidele/toris/internal/cluster"
	"github.com/tobibamidele/toris/pkg/model"
)

// newNodeAddCmd returns the 'toris node add' command.
func newNodeAddCmd() *cobra.Command {
	var (
		nodeID   string
		host     string
		port     int
		authProf string
	)

	cmd := &cobra.Command{
		Use:   "add",
		Short: "Add a node to the cluster registry",
		Long: `Registers a new node in the toris_control.nodes table.
The daemon's node watcher will pick up the change within ~30 seconds
and begin running health checks against the new node.

The node starts in 'joining' status and transitions to 'healthy' once
the daemon's health check loop confirms it is reachable.`,
		Example: `  toris node add --id node-03 --host pg-replica-3.example.com --port 5432`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if nodeID == "" {
				return fmt.Errorf("--id is required")
			}
			if host == "" {
				return fmt.Errorf("--host is required")
			}
			if port <= 0 || port > 65535 {
				return fmt.Errorf("--port must be between 1 and 65535, got %d", port)
			}

			cfg, log, err := loadConfig()
			if err != nil {
				return err
			}

			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()

			pool, poolErr := connectControlDB(ctx, cfg.ControlDSN)
			if poolErr != nil {
				return fmt.Errorf("connecting to control DB: %w", poolErr)
			}
			defer pool.Close()

			reg := cluster.New(log, pool, cfg.Cluster.ID)

			// Check if the node already exists.
			if err := reg.Load(ctx); err != nil {
				log.Warn("could not load existing registry", "error", err.Error())
			}
			if _, exists := reg.Get(nodeID); exists {
				return fmt.Errorf("node %q already exists in the registry; use 'toris node list' to see current nodes", nodeID)
			}

			node := cluster.NodeFromConfig(cfg.Cluster.ID, nodeID, host, port)
			if err := reg.Upsert(ctx, node); err != nil {
				return fmt.Errorf("adding node to registry: %w", err)
			}

			if cfg.OutputFormat == "json" || gFlags.outputFormat == "json" {
				printJSON(node)
			} else {
				fmt.Printf("✓ Node %s added to cluster %s\n", nodeID, cfg.Cluster.ID)
				fmt.Printf("  Host       : %s:%d\n", host, port)
				fmt.Printf("  Status     : joining\n")
				if authProf != "" {
					fmt.Printf("  Auth       : %s\n", authProf)
				}
				fmt.Println("  The daemon will pick up this node on the next registry sync (~30s).")
			}

			log.Info("node added to registry",
				"node_id", nodeID,
				"host", host,
				"port", port,
				"cluster_id", cfg.Cluster.ID,
			)
			return nil
		},
	}

	cmd.Flags().StringVar(&nodeID, "id", "", "unique node ID (required)")
	cmd.Flags().StringVar(&host, "host", "", "hostname or IP of the PostgreSQL node (required)")
	cmd.Flags().IntVar(&port, "port", 5432, "PostgreSQL port")
	cmd.Flags().StringVar(&authProf, "auth-profile", "", "named auth profile for this node")
	_ = cmd.MarkFlagRequired("id")
	_ = cmd.MarkFlagRequired("host")

	return cmd
}

// newNodeRemoveCmd returns the 'toris node remove' command.
func newNodeRemoveCmd() *cobra.Command {
	var nodeID string

	cmd := &cobra.Command{
		Use:   "remove",
		Short: "Remove a node from the cluster registry",
		Long: `Marks a node as 'removed' in toris_control.nodes.
The node will no longer be included in health checks or failover candidate selection.

This command refuses to remove a node that is currently the active primary
unless --force is passed. Use 'toris demote' first to step down the primary
before removing it.`,
		Example: `  toris node remove --id node-02
  toris node remove --id node-02 --force   # skips confirmation`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if nodeID == "" {
				return fmt.Errorf("--id is required")
			}

			cfg, log, err := loadConfig()
			if err != nil {
				return err
			}

			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()

			if !gFlags.force {
				ok, _ := confirmDestructive(fmt.Sprintf("remove node %s from cluster %s?", nodeID, cfg.Cluster.ID))
				if !ok {
					fmt.Println("aborted")
					return nil
				}
			}

			pool, poolErr := connectControlDB(ctx, cfg.ControlDSN)
			if poolErr != nil {
				return fmt.Errorf("connecting to control DB: %w", poolErr)
			}
			defer pool.Close()

			reg := cluster.New(log, pool, cfg.Cluster.ID)
			if err := reg.Load(ctx); err != nil {
				return fmt.Errorf("loading registry: %w", err)
			}

			node, exists := reg.Get(nodeID)
			if !exists {
				return fmt.Errorf("node %q not found in registry for cluster %s", nodeID, cfg.Cluster.ID)
			}

			// Guard against removing the active primary.
			if node.Role == model.NodeRolePrimary &&
				node.Status != model.NodeStatusFenced &&
				node.Status != model.NodeStatusRemoved {
				if !gFlags.force {
					return fmt.Errorf(
						"node %s is the active primary — demote it first with 'toris demote', "+
							"or pass --force to remove it anyway (dangerous: will cause downtime)",
						nodeID)
				}
				log.Warn("removing active primary — operator used --force",
					"node_id", nodeID,
					"role", node.Role,
				)
			}

			if err := reg.Remove(ctx, nodeID); err != nil {
				return fmt.Errorf("removing node: %w", err)
			}

			if cfg.OutputFormat == "json" || gFlags.outputFormat == "json" {
				printJSON(map[string]string{
					"node_id":    nodeID,
					"cluster_id": cfg.Cluster.ID,
					"status":     "removed",
				})
			} else {
				fmt.Printf("✓ Node %s removed from cluster %s\n", nodeID, cfg.Cluster.ID)
				fmt.Println("  The daemon will stop health-checking this node on the next registry sync.")
			}

			log.Info("node removed from registry",
				"node_id", nodeID,
				"cluster_id", cfg.Cluster.ID,
			)
			return nil
		},
	}

	cmd.Flags().StringVar(&nodeID, "id", "", "ID of the node to remove (required)")
	_ = cmd.MarkFlagRequired("id")

	return cmd
}
