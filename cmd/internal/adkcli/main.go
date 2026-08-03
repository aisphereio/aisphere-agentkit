// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"context"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"sort"

	"google.golang.org/adk/agent"
	"google.golang.org/adk/cmd/launcher"
	"google.golang.org/adk/cmd/launcher/full"
	"google.golang.org/adk/internal/aihubruntime"
	"google.golang.org/adk/internal/configurable"
	"google.golang.org/adk/internal/configurable/conformance"
	"google.golang.org/adk/internal/configurable/conformance/replayplugin"
	"google.golang.org/adk/internal/runtimeconfig"
	"google.golang.org/adk/internal/runtimetrace"
	"google.golang.org/adk/internal/skillservice"
	"google.golang.org/adk/plugin"
	"google.golang.org/adk/runner"
)

func findAgentConfigs(root string) ([]string, error) {
	configs := []string{}
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(rootAbs); os.IsNotExist(err) {
		return configs, nil
	}

	err = filepath.WalkDir(rootAbs, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			fmt.Printf("Warning: skipping %q due to error: %v\n", path, err)
			return nil
		}
		if d.IsDir() {
			switch d.Name() {
			case ".adk", ".git", "node_modules", "vendor":
				return filepath.SkipDir
			}
			return nil
		}
		if d.Name() != "root_agent.yaml" {
			return nil
		}
		if filepath.Clean(filepath.Dir(path)) == rootAbs {
			fmt.Printf("⚠️  Ignoring root_agent.yaml directly under apps root: %s. Put it under agents/<app>/root_agent.yaml.\n", path)
			return nil
		}
		configs = append(configs, path)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(configs)
	return configs, nil
}

func main() {
	// 1. Get the Current Working Directory (where the user typed 'adk')
	cwd, err := os.Getwd()
	if err != nil {
		log.Fatalf("Error getting current directory: %v", err)
	}

	appConfig, err := runtimeconfig.Load(cwd, "")
	if err != nil {
		log.Fatalf("Error loading runtime config: %v", err)
	}
	fmt.Printf("⚙️  Runtime config loaded")
	if appConfig.ConfigPath != "" {
		fmt.Printf(": %s", appConfig.ConfigPath)
	}
	fmt.Println()

	services, err := appConfig.BuildServices()
	if err != nil {
		log.Fatalf("Error creating configured services: %v", err)
	}
	traceRecorder, err := runtimetrace.NewFileRecorder(runtimetrace.Config{
		Enabled:         appConfig.Runtime.Tracing.Enabled,
		Root:            appConfig.Runtime.Tracing.Root,
		DumpLLMRequest:  appConfig.Runtime.Tracing.DumpLLMRequest,
		DumpLLMResponse: appConfig.Runtime.Tracing.DumpLLMResponse,
		DumpToolEvents:  appConfig.Runtime.Tracing.DumpToolEvents,
		DumpStream:      appConfig.Runtime.Tracing.DumpStream,
		MaxContentChars: appConfig.Runtime.Tracing.MaxContentChars,
	})
	if err != nil {
		log.Fatalf("Error creating runtime trace recorder: %v", err)
	}
	if traceRecorder.Enabled() {
		fmt.Printf("🧭 Runtime trace enabled: %s\n", traceRecorder.Root())
	}

	skillSvc, err := skillservice.NewFileSystemService(appConfig.Skills.Root)
	if err != nil {
		log.Fatalf("Error creating skill service: %v", err)
	}
	if appConfig.Skills.Enabled {
		fmt.Printf("📚 Skill service enabled: %s\n", skillSvc.Root())
	}

	ctx := runtimeconfig.WithConfig(context.Background(), appConfig)
	if appConfig.Skills.AIHub.Enabled && !appConfig.Skills.AIHub.AgentMode && appConfig.Skills.AIHub.SyncOnStart {
		client, err := aihubruntime.New(appConfig.Skills.AIHub)
		if err != nil {
			log.Fatalf("Error creating AIHub skill sync client: %v", err)
		}
		if client != nil {
			result, err := client.SyncSkills(ctx, skillSvc)
			if err != nil {
				log.Fatalf("Error syncing skills from AIHub: %v", err)
			}
			fmt.Printf("🔄 AIHub skill sync: revision=%s discovered=%d downloaded=%d activated=%v skipped=%v\n", result.Revision, result.Discovered, result.Downloaded, result.Activated, result.Skipped)
			client.Watch(ctx, skillSvc, func(result *aihubruntime.SyncResult) {
				if result != nil {
					log.Printf("🔁 AIHub skill catalog changed: skillset=%s revision=%s downloaded=%d activated=%v pruned=%v", result.SkillSet, result.Revision, result.Downloaded, result.Activated, result.Pruned)
				}
			})
			if err := client.ReportInstalledSkills(ctx, skillSvc, result); err != nil {
				log.Printf("⚠️  AIHub installed-skill report failed: %v", err)
			}
		}
	}

	ctx = runtimetrace.WithRecorder(ctx, traceRecorder)

	// Register callbacks for the conformance agents
	err = conformance.RegisterCallbacks()
	if err != nil {
		log.Fatalf("Error registering callbacks: %v", err)
	}
	err = conformance.RegisterFunctions()
	if err != nil {
		log.Fatalf("Error registering functions: %v", err)
	}

	var loader agent.Loader
	if appConfig.Skills.AIHub.Enabled && appConfig.Skills.AIHub.AgentMode {
		hubClient, err := aihubruntime.New(appConfig.Skills.AIHub)
		if err != nil {
			log.Fatalf("Error creating AIHub agent client: %v", err)
		}
		loader, err = aihubruntime.NewSessionAgentLoader(hubClient, appConfig)
		if err != nil {
			log.Fatalf("Error creating AIHub agent loader: %v", err)
		}
		fmt.Println("AIHub Agent mode enabled: agents and skills resolve per authenticated session")
	}

	if !appConfig.Skills.AIHub.AgentMode {
		scanRoot := appConfig.Builder.AppsRoot
		if scanRoot == "" {
			scanRoot = filepath.Join(cwd, "agents")
		}
		fmt.Printf("🔍 Scanning for 'root_agent.yaml' in apps root: %s\n", scanRoot)

		// 2. Only scan the configured apps root. Do not recursively scan the whole
		// project, otherwise .adk/builder/tmp drafts or misplaced root_agent.yaml
		// files become runtime apps. The supported layout is:
		//   agents/<app_name>/root_agent.yaml
		agentConfigs, err := findAgentConfigs(scanRoot)
		if err != nil {
			log.Fatalf("Error scanning agent configs: %v", err)
		}

		// 3. Check if we found anything
		if len(agentConfigs) == 0 {
			fmt.Printf("❌ No 'root_agent.yaml' files found in %s. Put apps under agents/<app>/root_agent.yaml.\n", scanRoot)
			os.Exit(1)
		}

		fmt.Printf("🚀 Found %d agent config(s)\n", len(agentConfigs))
		agentsMap := make(map[string]agent.Agent, len(agentConfigs))
		// 4. Iterate and Load all agents found
		for _, configPath := range agentConfigs {
			fmt.Printf("➡️  Loading agent from: %s\n", configPath)

			// This reads the YAML, finds the 'agent_class', and calls the registered factory.
			myAgent, err := configurable.FromConfig(ctx, configPath)
			if err != nil {
				log.Printf("⚠️  Error loading agent at %s: %v", configPath, err)
				continue // Skip this one and try the next
			}
			fmt.Printf("✅ Agent loaded successfully: %s\n", myAgent.Name())

			folderName := filepath.Base(filepath.Dir(configPath))
			fmt.Printf("✅ Agent folder name: %s\n", folderName)

			if _, ok := agentsMap[folderName]; ok {
				log.Printf("⚠️  Agent %s already exists, skipping", folderName)
				continue
			}
			agentsMap[folderName] = myAgent
		}

		if len(agentsMap) == 0 {
			log.Fatalf("No agents were loaded successfully. Check the warnings above for root_agent.yaml parse/model/tool errors.")
		}

		loader, err = conformance.NewConformanceAgentLoader(agentsMap)
		if err != nil {
			log.Fatalf("Error loading agent: %v", err)
		}
	}

	config := &launcher.Config{
		AgentLoader:         loader,
		SessionService:      services.Session,
		ArtifactService:     services.Artifact,
		MemoryService:       services.Memory,
		BuilderAppsRoot:     appConfig.Builder.AppsRoot,
		BuilderTmpRoot:      appConfig.Builder.TmpRoot,
		BuilderDefaultModel: appConfig.Builder.DefaultModel,
		RuntimeConfig:       appConfig,
		TraceRecorder:       traceRecorder,
		SkillService:        skillSvc,
		PluginConfig: runner.PluginConfig{
			Plugins: []*plugin.Plugin{
				replayplugin.MustNew(cwd),
			},
		},
	}

	args := os.Args[1:]
	if len(args) == 0 {
		args = appConfig.DefaultLauncherArgs()
		fmt.Printf("▶️  No launcher args provided; using config/default args: %v\n", args)
	}

	l := full.NewLauncher()
	if err = l.Execute(ctx, config, args); err != nil {
		log.Fatalf("Run failed: %v\n\n%s", err, l.CommandLineSyntax())
	}
}
