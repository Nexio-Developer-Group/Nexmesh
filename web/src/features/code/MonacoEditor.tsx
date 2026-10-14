import { useEffect, useRef } from 'react'
import monaco from './monaco'

interface Props {
  path: string
  initial: string
  /** Changes when the editor must replace its text with `initial` (reload). */
  revision: number
  line?: number
  onChange: (text: string) => void
  onSave: () => void
}

/** One Monaco instance; each open file keeps its own model so undo history survives tab switches. */
export default function MonacoEditor({ path, initial, revision, line, onChange, onSave }: Props) {
  const host = useRef<HTMLDivElement>(null)
  const editor = useRef<monaco.editor.IStandaloneCodeEditor | null>(null)
  const cb = useRef({ onChange, onSave })
  cb.current = { onChange, onSave }

  useEffect(() => {
    const ed = monaco.editor.create(host.current!, {
      theme: 'nexmesh-dark', automaticLayout: true, fontFamily: "'JetBrains Mono', ui-monospace, monospace", fontSize: 13,
      minimap: { enabled: false }, scrollBeyondLastLine: false, model: null, tabSize: 2, renderWhitespace: 'selection',
    })
    editor.current = ed
    ed.addCommand(monaco.KeyMod.CtrlCmd | monaco.KeyCode.KeyS, () => cb.current.onSave())
    return () => { ed.dispose(); monaco.editor.getModels().forEach((m) => m.dispose()); editor.current = null }
  }, [])

  // Switch model when the active file changes.
  useEffect(() => {
    const ed = editor.current
    if (!ed) return
    const uri = monaco.Uri.from({ scheme: 'file', path: '/' + path })
    let model = monaco.editor.getModel(uri)
    if (!model) model = monaco.editor.createModel(initial, undefined, uri)
    ed.setModel(model)
    const sub = model.onDidChangeContent(() => cb.current.onChange(model!.getValue()))
    return () => sub.dispose()
    // `initial` is only the seed for a new model; later text lives in the model.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [path])

  // Reload from disk.
  const lastRev = useRef(revision)
  useEffect(() => {
    if (lastRev.current === revision) return
    lastRev.current = revision
    const model = editor.current?.getModel()
    if (model && model.getValue() !== initial) model.setValue(initial)
  }, [revision, initial])

  useEffect(() => {
    if (line && editor.current) { editor.current.revealLineInCenter(line); editor.current.setPosition({ lineNumber: line, column: 1 }); editor.current.focus() }
  }, [line, path])

  return <div ref={host} className="h-full w-full" data-testid="monaco-host" />
}
