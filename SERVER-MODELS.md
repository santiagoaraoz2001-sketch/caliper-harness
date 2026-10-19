# Models & Servers

Use `/manage` to add and manage your server connections and models. Use `/status` for runtime activity and usage. Coding and Judge are roles assigned to a model connection, not server names.

In `/manage`, use `n` to add a server, `+` to add a model, `e` to edit, Backspace to remove, and `t` to test. Forms use Tab between fields, Ctrl+S to save, Ctrl+T to test, F2 for advanced options, and Escape to cancel.

## Set up with your preferred LLM

As a second option, open `/manage assisted` or press `g` in `/manage`. Press `c` to copy the complete setup prompt and paste it into your preferred LLM. It includes this computer's actual configuration paths, the connection format, native judge guidance and validation steps. A coding assistant with local access can inspect your existing servers and checkpoints; a chat-only assistant will need you to provide paths and command output. Caliper makes no model request and changes no settings when you open or copy this prompt. Review the proposed wiring before applying it, then restart Caliper to reload saved connections.

Caliper does not scan your computer or open ports looking for models or servers. Only saved connections and services Caliper started are listed. Existing selected model/server settings remain available. Old `serve.model_dirs` settings remain readable but no longer populate the list.

## Connect a server

Open `/manage` and choose Add server. Select a preset such as MLX, Ollama or llama.cpp, or Custom for another compatible server. Give the connection a name and enter its actual URL. An already-running server needs no launch command. For a local service you want Caliper to start, choose its preset/executable; custom arguments are advanced settings.

Test checks the selected endpoint. A responding catalog does not prove a model is loaded or that inference works. Saved offline connections remain visible so you can start or correct them.

## Add a model

Select the saved server and choose Add model. Enter the exact ID that server accepts, or the exact local checkpoint folder/GGUF file used by your local launcher. A server's model catalog can supply IDs; no filesystem scan is required. For split safetensors, select the checkpoint folder containing its index; for split GGUF, select the first shard. The server must support the checkpoint's architecture and format.

Choose Use for Coding or Use for Judge to assign the saved model. Decision-trained models are recommended for judging; a connection test does not qualify judgment quality. Settings can disable the judge, in which case required human decisions use the existing approval flow.

## Start, load and stop

Start uses the saved server launch command. Load and Unload manage memory residency through the supported runtime or Caliper-owned service. These actions do not download model weights. Servers managed by another application remain external; unsupported lifecycle actions must be performed in that application. Only processes Caliper owns may be stopped by Caliper.

Edit or Remove changes the saved connection, not the checkpoint files. Reusable connections are stored in the OS user configuration directory at `caliper/connections.yaml`. Explicit project connection settings continue to override machine defaults.

## Status

`/status` reports current conversation activity, reported generated tokens, recorded coding requests, tool calls, model identities and the latest observed connection state. Request throughput includes prefill and transport, and excludes time spent executing tools. Counts cover retained recorded coding runs; judge requests, title generation and manual compaction are excluded. Missing usage stays unknown or partial; resumed history without measurements is labelled accordingly. Use `/manage` for configuration and lifecycle actions.
