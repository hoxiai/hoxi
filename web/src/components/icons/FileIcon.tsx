import type { Component, JSX } from "solid-js"
import { createMemo, createUniqueId, splitProps, Show } from "solid-js"
import sprite from "./file-icons/sprite.svg"
import type { IconName } from "./file-icons/types"
import { ICON_MAPS } from "./file-icons/icon-maps"

export type FileIconProps = JSX.GSVGAttributes<SVGSVGElement> & {
  node: { path: string; type: "file" | "directory" }
  expanded?: boolean
  mono?: boolean
}

export const FileIcon: Component<FileIconProps> = (props) => {
  const [local, rest] = splitProps(props, ["node", "class", "classList", "expanded", "mono"])
  const name = createMemo(() => chooseIconName(local.node.path, local.node.type, local.expanded || false))
  const id = `file-icon-mono-${createUniqueId()}`
  return (
    <svg
      data-component="file-icon"
      {...rest}
      classList={{
        ...local.classList,
        [local.class ?? ""]: !!local.class,
      }}
    >
      <Show when={local.mono} fallback={<use href={`${sprite}#${name()}`} />}>
        <defs>
          <mask id={id} mask-type="alpha">
            <use href={`${sprite}#${name()}`} />
          </mask>
        </defs>
        <rect width="100%" height="100%" fill="currentColor" mask={`url(#${id})`} />
      </Show>
    </svg>
  )
}

const toOpenVariant = (icon: IconName): IconName => {
  if (!icon.startsWith("Folder")) return icon
  if (icon.endsWith("_light")) return icon.replace("_light", "Open_light") as IconName
  if (!icon.endsWith("Open")) return (icon + "Open") as IconName
  return icon
}

const basenameOf = (p: string) => p.split("\\").join("/").split("/").filter(Boolean).pop() ?? ""

const folderNameVariants = (name: string) => {
  const n = name.toLowerCase()
  return [n, `.${n}`, `_${n}`, `__${n}__`]
}

const dottedSuffixesDesc = (name: string) => {
  const n = name.toLowerCase()
  const idxs: number[] = []
  for (let i = 0; i < n.length; i++) if (n[i] === ".") idxs.push(i)
  const out = new Set<string>()
  out.add(n) // allow exact whole-name "extensions" like "dockerfile"
  for (const i of idxs) if (i + 1 < n.length) out.add(n.slice(i + 1))
  return Array.from(out).sort((a, b) => b.length - a.length) // longest first
}

export function chooseIconName(path: string, type: "directory" | "file", expanded: boolean): IconName {
  const base = basenameOf(path)
  const baseLower = base.toLowerCase()

  if (type === "directory") {
    for (const cand of folderNameVariants(baseLower)) {
      const icon = ICON_MAPS.folderNames[cand]
      if (icon) return expanded ? toOpenVariant(icon) : icon
    }
    return expanded ? ICON_MAPS.defaults.folderOpen : ICON_MAPS.defaults.folder
  }

  const byName = ICON_MAPS.fileNames[baseLower]
  if (byName) return byName

  for (const ext of dottedSuffixesDesc(baseLower)) {
    const icon = ICON_MAPS.fileExtensions[ext]
    if (icon) return icon
  }

  return ICON_MAPS.defaults.file
}
