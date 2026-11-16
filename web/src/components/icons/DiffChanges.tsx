import { createMemo, For, Match, Show, Switch } from "solid-js"

export function DiffChanges(props: {
  class?: string
  changes: { additions: number; deletions: number } | Array<{ additions: number; deletions: number }>
  variant?: "default" | "bars"
  showZero?: boolean
}) {
  const variant = () => props.variant ?? "default"

  const additions = createMemo(() =>
    Array.isArray(props.changes)
      ? props.changes.reduce((acc, diff) => acc + (diff.additions || 0), 0)
      : props.changes.additions || 0,
  )
  const deletions = createMemo(() =>
    Array.isArray(props.changes)
      ? props.changes.reduce((acc, diff) => acc + (diff.deletions || 0), 0)
      : props.changes.deletions || 0,
  )
  const total = createMemo(() => additions() + deletions())

  const blockCounts = createMemo(() => {
    const TOTAL_BLOCKS = 5
    const adds = additions()
    const dels = deletions()
    if (adds === 0 && dels === 0) return { added: 0, deleted: 0, neutral: TOTAL_BLOCKS }
    const totalChanges = adds + dels
    if (totalChanges < 5) {
      const added = adds > 0 ? 1 : 0
      const deleted = dels > 0 ? 1 : 0
      return { added, deleted, neutral: TOTAL_BLOCKS - added - deleted }
    }
    const ratio = adds > dels ? adds / dels : dels / adds
    let blocksForChanges = TOTAL_BLOCKS
    if (totalChanges < 20) blocksForChanges = TOTAL_BLOCKS - 2
    else if (totalChanges < 50) blocksForChanges = TOTAL_BLOCKS - 1

    const percentAdded = adds / totalChanges
    const percentDeleted = dels / totalChanges
    const added_raw = percentAdded * blocksForChanges
    const deleted_raw = percentDeleted * blocksForChanges
    let added = adds > 0 ? Math.max(1, Math.round(added_raw)) : 0
    let deleted = dels > 0 ? Math.max(1, Math.round(deleted_raw)) : 0

    if (adds > 0 && dels > 0 && ratio > 10) {
      if (adds > dels) deleted = 1
      else added = 1
    }
    if (added + deleted > blocksForChanges) {
      if (added_raw > deleted_raw) added = blocksForChanges - deleted
      else deleted = blocksForChanges - added
    }
    const neutral = TOTAL_BLOCKS - added - deleted
    return { added, deleted, neutral }
  })

  const BLOCK_COLORS = {
    added: "var(--status-success)",
    deleted: "var(--status-danger)",
    neutral: "var(--border-strong)",
  } as const

  const visibleBlocks = createMemo(() => {
    const counts = blockCounts()
    return [
      ...Array(counts.added).fill(BLOCK_COLORS.added),
      ...Array(counts.deleted).fill(BLOCK_COLORS.deleted),
      ...Array(counts.neutral).fill(BLOCK_COLORS.neutral),
    ]
  })

  return (
    <Switch>
      <Match when={variant() === "bars"}>
        <svg
          data-component="diff-changes"
          data-variant="bars"
          class={props.class}
          viewBox="0 0 18 12"
          fill="none"
          xmlns="http://www.w3.org/2000/svg"
        >
          <g>
            <For each={visibleBlocks()}>
              {(color, i) => <rect x={i() * 4} width="2" height="12" rx="1" fill={color} />}
            </For>
          </g>
        </svg>
      </Match>
      <Match when={variant() === "default"}>
        <Show when={total() > 0 || props.showZero}>
          <div data-component="diff-changes" class={props.class}>
            <span data-slot="diff-changes-additions">{`+${additions()}`}</span>
            <span data-slot="diff-changes-deletions">{`-${deletions()}`}</span>
          </div>
        </Show>
      </Match>
    </Switch>
  )
}
