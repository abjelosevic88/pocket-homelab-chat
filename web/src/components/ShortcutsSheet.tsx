import { Modal } from './ui'

const GROUPS: { title: string; keys: [string[], string][] }[] = [
  { title: 'Go to', keys: [[['h'], 'Overview'], [['o'], 'Posts'], [['c'], 'Comments'], [['i'], 'Pictures'], [['r'], 'Readers'], [['l'], 'Look & details'], [[','], 'Settings'], [['/'], 'Search & commands'], [['⌘', 'K'], 'Search & commands']] },
  { title: 'Write', keys: [[['n'], 'A new post'], [['p'], 'A new page'], [['a'], 'Start from your apps']] },
  { title: 'In the editor', keys: [[['/'], 'Add a heading, list, quote, picture or table (at the start of a line)'], [['⌘', 'S'], 'Save now'], [['⌘', 'B'], 'Bold'], [['⌘', 'I'], 'Italic'], [['select text'], 'Formatting and links for that part']] },
  { title: 'Window', keys: [[['['], 'Show or hide the sidebar'], [['esc'], 'Close a dialog or menu']] },
  { title: 'Help', keys: [[['?'], 'This sheet']] },
]

export function ShortcutsSheet({ onClose }: { onClose: () => void }) {
  return (
    <Modal title="Keyboard shortcuts" onClose={onClose} size="lg">
      <div className="shortcuts">
        {GROUPS.map(g => (
          <section key={g.title}>
            <h4 className="sc-h">{g.title}</h4>
            {g.keys.map(([keys, label]) => (
              <div key={label + keys.join('')} className="sc-row"><span>{label}</span><span className="sc-keys">{keys.map(k => <kbd key={k}>{k}</kbd>)}</span></div>
            ))}
          </section>
        ))}
      </div>
    </Modal>
  )
}
