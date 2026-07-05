// reponite VS Code extension — thin UI over the reponite CLI (roadmap 3.4).
// Commands run `reponite brief|compat <symbol-under-cursor>` in the workspace
// and show the JSON in an output channel; a command opens the web dashboard.
// The MCP server (`reponite mcp`) remains the richer agent-facing backend; this
// is the human-facing surface.
//
// Build: `npm install && npm run compile` (produces out/extension.js), then F5.
import * as vscode from 'vscode';
import { execFile } from 'child_process';

function config<T>(key: string, fallback: T): T {
  return vscode.workspace.getConfiguration('reponite').get<T>(key, fallback);
}

function run(args: string[], cwd: string): Promise<string> {
  return new Promise((resolve, reject) => {
    execFile(config('binary', 'reponite'), args, { cwd, maxBuffer: 16 * 1024 * 1024 }, (err, stdout, stderr) => {
      if (err) {
        reject(new Error(stderr.trim() || err.message));
        return;
      }
      resolve(stdout);
    });
  });
}

function workspaceDir(uri: vscode.Uri): string | undefined {
  const folder = vscode.workspace.getWorkspaceFolder(uri);
  return folder?.uri.fsPath ?? vscode.workspace.workspaceFolders?.[0]?.uri.fsPath;
}

const channel = vscode.window.createOutputChannel('reponite');

async function runForSymbol(title: string, subcommand: string): Promise<void> {
  const editor = vscode.window.activeTextEditor;
  if (!editor) {
    vscode.window.showWarningMessage('reponite: open a file and place the cursor on a symbol.');
    return;
  }
  const range = editor.document.getWordRangeAtPosition(editor.selection.active);
  const symbol = range ? editor.document.getText(range) : editor.document.getText(editor.selection);
  if (!symbol) {
    vscode.window.showWarningMessage('reponite: place the cursor on a symbol first.');
    return;
  }
  const cwd = workspaceDir(editor.document.uri);
  if (!cwd) {
    vscode.window.showWarningMessage('reponite: no workspace folder.');
    return;
  }
  channel.clear();
  channel.show(true);
  channel.appendLine(`# reponite ${title}: ${symbol}`);
  try {
    channel.appendLine(await run([subcommand, symbol], cwd));
  } catch (e) {
    channel.appendLine('error: ' + (e instanceof Error ? e.message : String(e)));
    channel.appendLine('(is the repo indexed? run `reponite index .`)');
  }
}

export function activate(context: vscode.ExtensionContext): void {
  context.subscriptions.push(
    channel,
    vscode.commands.registerCommand('reponite.brief', () => runForSymbol('brief', 'brief')),
    vscode.commands.registerCommand('reponite.compat', () => runForSymbol('compat', 'compat')),
    vscode.commands.registerCommand('reponite.serve', () => {
      const cwd = vscode.workspace.workspaceFolders?.[0]?.uri.fsPath;
      if (!cwd) {
        vscode.window.showWarningMessage('reponite: no workspace folder.');
        return;
      }
      const addr = config('serveAddr', '127.0.0.1:8899');
      const term = vscode.window.createTerminal('reponite serve');
      term.sendText(`${config('binary', 'reponite')} serve --addr ${addr}`);
      term.show();
      vscode.env.openExternal(vscode.Uri.parse(`http://${addr}`));
    }),
  );
}

export function deactivate(): void {}
