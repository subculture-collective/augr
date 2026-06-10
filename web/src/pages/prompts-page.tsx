import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { AlertCircle, Check, RotateCcw, Save, Sparkles, SlidersHorizontal, Undo2 } from 'lucide-react';
import { useEffect, useMemo, useState } from 'react';

import { PageHeader } from '@/components/layout/page-header';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import { Label } from '@/components/ui/label';
import { Textarea } from '@/components/ui/textarea';
import { ConsolePanel, HudBadge, HudSection, StatusLed } from '@/components/ui/hud';
import { ApiClientError, apiClient } from '@/lib/api/client';
import type { PromptDefinition, PromptSettings, PromptSettingsUpdateRequest } from '@/lib/api/types';
import { cn } from '@/lib/utils';

const DRAFT_QUERY_KEY = ['prompts'] as const;

function formatCategory(category: string) {
  return category
    .split(/[_-]/g)
    .filter(Boolean)
    .map((part) => part[0].toUpperCase() + part.slice(1))
    .join(' ');
}

function extractOverrides(prompts: PromptDefinition[]) {
  return Object.fromEntries(prompts.filter((prompt) => prompt.overridden).map((prompt) => [prompt.key, prompt.override_text]));
}

function getDirtyKeys(draft: Record<string, string>, saved: Record<string, string>) {
  return Array.from(new Set([...Object.keys(draft), ...Object.keys(saved)])).filter(
    (key) => draft[key] !== saved[key],
  );
}

function getPromptSuffix(key: string) {
  const parts = key.split(/[._-]/g).filter(Boolean);
  return parts.at(-1) ?? key;
}

function getPromptNamespace(key: string, category: string) {
  const parts = key.split(/[._-]/g).filter(Boolean);
  return parts[0] ?? category;
}

function getPromptTags(prompt: PromptDefinition, hasDraftChange: boolean) {
  const tags = [formatCategory(prompt.category), getPromptNamespace(prompt.key, prompt.category), getPromptSuffix(prompt.key)];

  if (prompt.overridden) {
    tags.push('saved override');
  } else {
    tags.push('default');
  }

  if (hasDraftChange) {
    tags.push('draft changed');
  }

  return Array.from(new Set(tags));
}

function summarizePromptText(text: string) {
  const trimmed = text.trim();

  if (!trimmed) {
    return {
      excerpt: '<empty>',
      lineCount: 0,
      charCount: 0,
    };
  }

  const lines = trimmed
    .split(/\r?\n/g)
    .map((line) => line.trim())
    .filter(Boolean);

  const excerpt = lines.slice(0, 4).join(' ');

  return {
    excerpt: excerpt.length > 240 ? `${excerpt.slice(0, 240).trimEnd()}…` : excerpt,
    lineCount: lines.length,
    charCount: trimmed.length,
  };
}

function buildMockPreview(prompt: PromptDefinition, effectiveText: string, previewMode: string) {
  const summary = summarizePromptText(effectiveText);

  return {
    sampleRequest: prompt.description || `Sample request for ${prompt.label}`,
    promptExcerpt: summary.excerpt,
    lineCount: summary.lineCount,
    charCount: summary.charCount,
    mockResponse:
      summary.excerpt === '<empty>'
        ? 'Local preview only: no effective instructions are available yet.'
        : `Local preview only: this ${previewMode} would shape a response for ${prompt.label} without calling the backend or an LLM.`,
  };
}

function DetailTile({ label, value }: { label: string; value: string }) {
  return (
    <div className="rounded-lg border border-border bg-background/80 p-3">
      <p className="font-mono text-[10px] uppercase tracking-[0.18em] text-muted-foreground">
        {label}
      </p>
      <p className="mt-2 text-sm text-foreground">{value}</p>
    </div>
  );
}

function PromptBody({ title, text, badge }: { title: string; text: string; badge?: string }) {
  const displayText = text.length ? text : '<empty>';

  return (
    <div className="rounded-xl border border-border bg-background/85 p-4 shadow-sm shadow-black/5">
      <div className="flex items-center justify-between gap-3">
        <p className="font-medium text-foreground">{title}</p>
        {badge ? <Badge variant="outline">{badge}</Badge> : null}
      </div>
      <pre className="mt-3 max-h-64 overflow-auto whitespace-pre-wrap break-words font-mono text-[13px] leading-6 text-muted-foreground">
        {displayText}
      </pre>
    </div>
  );
}

function PromptTags({ tags }: { tags: string[] }) {
  return (
    <div className="flex flex-wrap gap-1.5">
      {tags.map((tag) => (
        <Badge
          key={tag}
          variant={tag === 'draft changed' ? 'warning' : tag === 'saved override' ? 'success' : 'outline'}
          className="text-[10px] uppercase tracking-[0.16em]"
        >
          {tag}
        </Badge>
      ))}
    </div>
  );
}

export function PromptsPage() {
  const queryClient = useQueryClient();
  const [selectedCategory, setSelectedCategory] = useState<string>('all');
  const [selectedKey, setSelectedKey] = useState<string | null>(null);
  const [draftOverrides, setDraftOverrides] = useState<Record<string, string>>({});
  const [saveMessage, setSaveMessage] = useState<string | null>(null);
  const [saveError, setSaveError] = useState<string | null>(null);

  const promptsQuery = useQuery({
    queryKey: DRAFT_QUERY_KEY,
    queryFn: () => apiClient.getPrompts(),
  });

  const prompts = useMemo(() => promptsQuery.data?.prompts ?? [], [promptsQuery.data?.prompts]);

  const categories = useMemo(
    () => Array.from(new Set(prompts.map((prompt) => prompt.category))).sort((left, right) => left.localeCompare(right)),
    [prompts],
  );

  const filteredPrompts = useMemo(
    () => (selectedCategory === 'all' ? prompts : prompts.filter((prompt) => prompt.category === selectedCategory)),
    [prompts, selectedCategory],
  );

  const selectedPrompt = useMemo(
    () => filteredPrompts.find((prompt) => prompt.key === selectedKey) ?? null,
    [filteredPrompts, selectedKey],
  );

  const savedOverrides = useMemo(() => extractOverrides(prompts), [prompts]);
  const dirtyKeys = useMemo(() => getDirtyKeys(draftOverrides, savedOverrides), [draftOverrides, savedOverrides]);
  const dirtyKeySet = useMemo(() => new Set(dirtyKeys), [dirtyKeys]);
  const isDirty = dirtyKeys.length > 0;

  useEffect(() => {
    if (!prompts.length) {
      setSelectedKey(null);
      return;
    }

    if (selectedCategory !== 'all' && !categories.includes(selectedCategory)) {
      setSelectedCategory('all');
      return;
    }

    if (!filteredPrompts.length) {
      setSelectedKey(null);
      return;
    }

    setSelectedKey((current) => {
      if (current && filteredPrompts.some((prompt) => prompt.key === current)) {
        return current;
      }

      return filteredPrompts[0]?.key ?? null;
    });
  }, [categories, filteredPrompts, prompts.length, selectedCategory]);

  useEffect(() => {
    if (promptsQuery.data) {
      setDraftOverrides(extractOverrides(promptsQuery.data.prompts));
      setSaveMessage(null);
      setSaveError(null);
    }
  }, [promptsQuery.data]);

  const saveMutation = useMutation({
    mutationFn: (payload: PromptSettingsUpdateRequest) => apiClient.updatePrompts(payload),
    onSuccess: (updatedSettings: PromptSettings) => {
      queryClient.setQueryData(DRAFT_QUERY_KEY, updatedSettings);
      setDraftOverrides(extractOverrides(updatedSettings.prompts));
      setSaveMessage('Prompt overrides saved.');
      setSaveError(null);
    },
    onError: (error) => {
      setSaveMessage(null);
      setSaveError(error instanceof ApiClientError ? error.message : 'Unable to save prompt overrides.');
    },
  });

  function updatePromptOverride(key: string, value: string) {
    setDraftOverrides((current) => ({
      ...current,
      [key]: value,
    }));
    setSaveMessage(null);
    setSaveError(null);
  }

  function resetAllPrompts() {
    setDraftOverrides(savedOverrides);
    setSaveMessage(null);
    setSaveError(null);
  }

  function resetPromptDraft(key: string) {
    setDraftOverrides((current) => {
      const next = { ...current };

      if (Object.prototype.hasOwnProperty.call(savedOverrides, key)) {
        next[key] = savedOverrides[key];
      } else {
        delete next[key];
      }

      return next;
    });
    setSaveMessage(null);
    setSaveError(null);
  }

  function handleSave() {
    saveMutation.mutate({ overrides: draftOverrides });
  }

  if (promptsQuery.isLoading) {
    return (
      <div className="space-y-6" data-testid="prompts-page-loading">
        <div className="h-24 animate-pulse rounded-2xl border border-border bg-card" />
        <div className="grid gap-6 xl:grid-cols-[320px_minmax(0,1fr)]">
          <div className="h-[32rem] animate-pulse rounded-2xl border border-border bg-card" />
          <div className="h-[32rem] animate-pulse rounded-2xl border border-border bg-card" />
        </div>
      </div>
    );
  }

  if (promptsQuery.isError || (!promptsQuery.isLoading && !promptsQuery.data)) {
    return (
      <Card data-testid="prompts-page-error">
        <CardHeader>
          <CardTitle>Prompts</CardTitle>
          <CardDescription>Unable to load the prompt registry.</CardDescription>
        </CardHeader>
        <CardContent className="flex items-center gap-2 text-sm text-muted-foreground">
          <AlertCircle className="size-4 text-destructive" />
          Start the API server to inspect and edit prompt overrides.
        </CardContent>
      </Card>
    );
  }

  const currentOverride = selectedPrompt
    ? Object.prototype.hasOwnProperty.call(draftOverrides, selectedPrompt.key)
      ? draftOverrides[selectedPrompt.key]
      : ''
    : '';
  const hasCurrentOverride = selectedPrompt
    ? Object.prototype.hasOwnProperty.call(draftOverrides, selectedPrompt.key)
    : false;
  const savedOverride = selectedPrompt
    ? Object.prototype.hasOwnProperty.call(savedOverrides, selectedPrompt.key)
      ? savedOverrides[selectedPrompt.key]
      : ''
    : '';
  const hasSavedOverride = selectedPrompt
    ? Object.prototype.hasOwnProperty.call(savedOverrides, selectedPrompt.key)
    : false;
  const currentEffectiveText = selectedPrompt
    ? hasCurrentOverride
      ? currentOverride
      : selectedPrompt.default_text
    : '';
  const selectedPromptDirty = selectedPrompt
    ? currentOverride !== savedOverride || hasCurrentOverride !== hasSavedOverride
    : false;
  const selectedPreviewMode = selectedPromptDirty
    ? 'draft override'
    : hasCurrentOverride
      ? 'saved override'
      : 'default prompt';
  const selectedPromptTags = selectedPrompt ? getPromptTags(selectedPrompt, selectedPromptDirty) : [];
  const selectedPromptPreview = selectedPrompt ? buildMockPreview(selectedPrompt, currentEffectiveText, selectedPreviewMode) : null;

  return (
    <div className="relative space-y-6" data-testid="prompts-page">
      <div className="pointer-events-none absolute inset-x-0 top-[-4rem] -z-10 h-64 bg-[radial-gradient(circle_at_15%_20%,rgba(59,130,246,0.15),transparent_40%),radial-gradient(circle_at_80%_0%,rgba(16,185,129,0.11),transparent_36%)] blur-3xl" />

      <PageHeader
        eyebrow="Prompt studio"
        title="Prompts"
        description="Edit runtime prompt overrides without leaving the control room."
        meta={(
          <div className="flex flex-wrap items-center gap-2">
            <Badge variant="outline">{prompts.length} prompts</Badge>
            <Badge variant="outline">{categories.length} categories</Badge>
            {isDirty ? <Badge variant="warning">{dirtyKeys.length} unsaved</Badge> : <Badge variant="success">Synced</Badge>}
          </div>
        )}
        actions={(
          <div className="flex flex-wrap items-center gap-2">
            <Button
              type="button"
              onClick={handleSave}
              disabled={!isDirty || saveMutation.isPending}
              data-testid="prompts-save-button"
            >
              <Save className="size-4" />
              {saveMutation.isPending ? 'Saving…' : 'Save changes'}
            </Button>
            <Button
              type="button"
              variant="outline"
              onClick={() => {
                if (selectedPrompt) {
                  resetPromptDraft(selectedPrompt.key);
                }
              }}
              disabled={!selectedPrompt || !selectedPromptDirty || saveMutation.isPending}
              data-testid="prompts-revert-button"
            >
              <RotateCcw className="size-4" />
              Revert selected draft
            </Button>
            <Button
              type="button"
              variant="outline"
              onClick={resetAllPrompts}
              disabled={!isDirty || saveMutation.isPending}
              data-testid="prompts-reset-all-button"
            >
              <Sparkles className="size-4" />
              Revert all drafts
            </Button>
          </div>
        )}
      />

      {saveMessage || saveError ? (
        <div
          className={cn(
            'rounded-lg border px-4 py-3 text-sm',
            saveError
              ? 'border-destructive/30 bg-destructive/10 text-destructive'
              : 'border-emerald-500/30 bg-emerald-500/10 text-emerald-500',
          )}
        >
          {saveError ?? saveMessage}
        </div>
      ) : null}

      <div className="grid gap-6 xl:grid-cols-[320px_minmax(0,1fr)]">
        <Card className="overflow-hidden xl:sticky xl:top-20 xl:h-[calc(100vh-7rem)]">
          <CardHeader className="gap-3">
            <CardTitle className="flex items-center gap-2">
              <SlidersHorizontal className="size-4" />
              Prompt library
            </CardTitle>
            <CardDescription>Filter by category, then choose the prompt you want to tune.</CardDescription>
          </CardHeader>
          <CardContent className="flex h-full min-h-0 flex-col gap-4">
            <div className="flex flex-wrap gap-2">
              <Button
                type="button"
                variant={selectedCategory === 'all' ? 'default' : 'outline'}
                size="dense"
                onClick={() => setSelectedCategory('all')}
              >
                All
              </Button>
              {categories.map((category) => {
                const count = prompts.filter((prompt) => prompt.category === category).length;

                return (
                  <Button
                    key={category}
                    type="button"
                    variant={selectedCategory === category ? 'default' : 'outline'}
                    size="dense"
                    onClick={() => setSelectedCategory(category)}
                  >
                    {formatCategory(category)}
                    <span className="ml-1 text-[10px] opacity-70">{count}</span>
                  </Button>
                );
              })}
            </div>

            <div className="min-h-0 flex-1 overflow-auto pr-1">
              <div className="space-y-2">
                {filteredPrompts.map((prompt) => {
                  const isSelected = prompt.key === selectedPrompt?.key;
                  const promptDirty = dirtyKeySet.has(prompt.key);
                  const promptTags = getPromptTags(prompt, promptDirty);

                  return (
                    <div
                      key={prompt.key}
                      data-testid={`prompt-card-${prompt.key}`}
                      className={cn(
                        'rounded-xl border px-3 py-3 transition-all',
                        isSelected
                          ? 'border-primary/40 bg-primary/8 shadow-sm shadow-primary/5'
                          : 'border-border bg-background/70 hover:border-primary/20 hover:bg-accent/45',
                      )}
                    >
                      <button
                        type="button"
                        onClick={() => setSelectedKey(prompt.key)}
                        className="w-full text-left"
                      >
                        <div className="flex items-start justify-between gap-3">
                          <div className="min-w-0 flex-1 space-y-2">
                            <div className="flex items-start gap-2">
                              <p className="truncate text-sm font-medium text-foreground">{prompt.label}</p>
                              {isSelected ? <Badge variant="success">selected</Badge> : null}
                            </div>
                            <p className="truncate font-mono text-[11px] uppercase tracking-[0.14em] text-muted-foreground">
                              {prompt.key}
                            </p>
                            <PromptTags tags={promptTags} />
                          </div>
                          <Badge variant={prompt.overridden ? 'success' : 'outline'}>
                            {prompt.overridden ? 'saved' : 'default'}
                          </Badge>
                        </div>
                        <p className="mt-3 text-sm leading-6 text-muted-foreground">{prompt.description}</p>
                      </button>

                      <div className="mt-3 flex items-center justify-between gap-3 border-t border-border/60 pt-3 text-[11px] uppercase tracking-[0.14em] text-muted-foreground">
                        <span>{promptDirty ? 'Local draft changed' : 'Ready to edit'}</span>
                        {promptDirty ? (
                          <Button
                            type="button"
                            variant="ghost"
                            size="sm"
                            onClick={() => resetPromptDraft(prompt.key)}
                            data-testid={`prompt-undo-${prompt.key}`}
                          >
                            <Undo2 className="size-4" />
                            Undo
                          </Button>
                        ) : prompt.key === selectedPrompt?.key ? (
                          <span className="flex items-center gap-1.5 text-primary">
                            <Check className="size-3.5" />
                            Open
                          </span>
                        ) : (
                          <span>Open</span>
                        )}
                      </div>
                    </div>
                  );
                })}

                {!filteredPrompts.length ? (
                  <div className="rounded-xl border border-dashed border-border bg-background px-4 py-8 text-center text-sm text-muted-foreground">
                    No prompts found for this category.
                  </div>
                ) : null}
              </div>
            </div>
          </CardContent>
        </Card>

        <Card className="overflow-hidden">
          <CardHeader className="gap-3 sm:flex-row sm:items-start sm:justify-between">
            <div className="space-y-1">
              <CardTitle className="flex items-center gap-2">
                <Sparkles className="size-4 text-primary" />
                {selectedPrompt ? selectedPrompt.label : 'Select a prompt'}
              </CardTitle>
              <CardDescription>
                {selectedPrompt
                  ? selectedPrompt.description
                  : 'Pick a prompt on the left to inspect its default text, current override, and effective preview.'}
              </CardDescription>
            </div>

            {selectedPrompt ? (
              <div className="flex flex-wrap items-center gap-2" data-testid="selected-prompt-tags">
                {selectedPromptTags.map((tag) => (
                  <Badge
                    key={tag}
                    variant={tag === 'draft changed' ? 'warning' : tag === 'saved override' ? 'success' : 'outline'}
                  >
                    {tag}
                  </Badge>
                ))}
              </div>
            ) : null}
          </CardHeader>
          <CardContent>
            {selectedPrompt ? (
              <div className="space-y-5">
                <div className="grid gap-3 md:grid-cols-3">
                  <DetailTile label="Category" value={formatCategory(selectedPrompt.category)} />
                  <DetailTile label="Key" value={selectedPrompt.key} />
                  <DetailTile label="State" value={selectedPrompt.overridden ? 'Saved override active' : 'Default prompt active'} />
                </div>

                <div className="grid gap-4 xl:grid-cols-[minmax(0,1.2fr)_minmax(0,0.8fr)]">
                  <div className="space-y-2">
                    <Label htmlFor="prompt-override">Override text</Label>
                    <Textarea
                      id="prompt-override"
                      value={currentOverride}
                      onChange={(event) => updatePromptOverride(selectedPrompt.key, event.target.value)}
                      className="min-h-[28rem] font-mono text-[13px] leading-6"
                      placeholder="Write a replacement prompt here…"
                    />
                    <p className="text-xs text-muted-foreground">
                      Revert selected draft restores the last saved snapshot for this prompt; save when you want to write the new draft back.
                    </p>
                  </div>

                  <div className="space-y-4">
                    <ConsolePanel className="border-border/80 bg-panel/90 p-4 shadow-lg shadow-black/10" data-testid="prompt-preview-panel">
                      <div className="flex flex-wrap items-start justify-between gap-3">
                        <div className="space-y-1">
                          <StatusLed
                            state={selectedPromptDirty ? 'warn' : selectedPrompt.overridden ? 'sync' : 'ok'}
                            label="local preview"
                          />
                          <p className="text-xs text-muted-foreground">
                            Deterministic sample only. No backend or LLM call is made here.
                          </p>
                        </div>
                        <HudBadge tone={selectedPromptDirty ? 'caution' : selectedPrompt.overridden ? 'pulse' : 'ink'}>
                          {selectedPromptDirty ? 'draft active' : selectedPrompt.overridden ? 'saved override' : 'default text'}
                        </HudBadge>
                      </div>

                      {selectedPromptPreview ? (
                        <div className="mt-4 space-y-4">
                          <div className="grid gap-4 md:grid-cols-2">
                            <div className="space-y-2">
                              <HudSection label="Sample request" note="mock input" />
                              <p className="rounded-xl border border-border bg-background/70 px-4 py-3 text-sm leading-6 text-foreground">
                                {selectedPromptPreview.sampleRequest}
                              </p>
                            </div>

                            <div className="space-y-2">
                              <HudSection
                                label="Prompt excerpt"
                                note={`${selectedPromptPreview.lineCount} lines · ${selectedPromptPreview.charCount} chars`}
                              />
                              <pre className="max-h-44 overflow-auto rounded-xl border border-border bg-background/70 px-4 py-3 whitespace-pre-wrap break-words font-mono text-[12px] leading-6 text-muted-foreground">
                                {selectedPromptPreview.promptExcerpt}
                              </pre>
                            </div>
                          </div>

                          <div className="rounded-xl border border-border bg-background/70 px-4 py-3">
                            <div className="flex items-center gap-2">
                              <HudBadge tone="signal">mock output</HudBadge>
                              <span className="text-xs uppercase tracking-[0.16em] text-muted-foreground">
                                local only
                              </span>
                            </div>
                            <p className="mt-3 text-sm leading-6 text-foreground">
                              {selectedPromptPreview.mockResponse}
                            </p>
                          </div>
                        </div>
                      ) : null}
                    </ConsolePanel>

                    <div className="grid gap-4 md:grid-cols-2">
                      <PromptBody title="Default prompt" text={selectedPrompt.default_text} />
                      <PromptBody
                        title="Saved override"
                        text={hasSavedOverride ? savedOverride : 'No saved override'}
                        badge={selectedPrompt.overridden ? 'active' : 'empty'}
                      />
                    </div>
                  </div>
                </div>
              </div>
            ) : (
              <div className="flex min-h-[24rem] flex-col items-center justify-center gap-3 rounded-xl border border-dashed border-border bg-background/60 text-center">
                <div className="flex size-12 items-center justify-center rounded-full border border-border bg-card text-primary">
                  <AlertCircle className="size-5" />
                </div>
                <div className="max-w-md space-y-1">
                  <p className="font-medium text-foreground">No prompt selected</p>
                  <p className="text-sm text-muted-foreground">
                    Choose a category and prompt on the left to open the editor.
                  </p>
                </div>
              </div>
            )}
          </CardContent>
        </Card>
      </div>
    </div>
  );
}
