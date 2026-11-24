import { useState } from 'react'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogTitle } from '@/components/ui/dialog'
import { ApiError } from '@/lib/api/client'
import { useSshConfig } from '@/lib/api/useSshConfig'

function errorText(error: unknown): string {
  if (error instanceof ApiError) {
    if (error.status === 0) return 'Could not reach the server.'
    if (error.code === 'profile_not_running') return 'Start the profile to see its SSH config.'
    if (error.code === 'profile_not_found') return 'This profile no longer exists.'
  }
  return 'The SSH config could not be loaded.'
}

/** An SSH config button for one profile and the dialog it opens. The text is fetched once per opening and never refreshed. */
export function SshConfigDialog({ profile }: { profile: string }) {
  const [open, setOpen] = useState(false)
  const [copy, setCopy] = useState<'idle' | 'done' | 'failed'>('idle')
  const query = useSshConfig(profile, open)
  const data = query.data
  const host = data?.entries.find((e) => e.key.toLowerCase() === 'host')?.value

  async function onCopy() {
    try {
      if (!navigator.clipboard || !data) throw new Error('unavailable')
      await navigator.clipboard.writeText(data.raw)
      setCopy('done')
    } catch {
      setCopy('failed')
    }
  }

  let body
  if (query.isPending) {
    body = <p className="text-sm text-muted-foreground">Loading SSH config…</p>
  } else if (query.isError) {
    const retry = !(query.error instanceof ApiError && query.error.code === 'profile_not_running')
    body = (
      <div role="alert" className="flex flex-col items-start gap-2 rounded-lg border border-destructive px-3 py-2 text-sm">
        <p>{errorText(query.error)}</p>
        {retry && (
          <Button type="button" variant="outline" size="sm" onClick={() => void query.refetch()}>
            Retry
          </Button>
        )}
      </div>
    )
  } else if (data) {
    body = (
      <>
        <table className="w-full text-left text-sm">
          <caption className="sr-only">SSH settings of {profile}</caption>
          <thead>
            <tr className="border-b border-border text-muted-foreground">
              <th scope="col" className="py-1 pr-3 font-medium">
                Setting
              </th>
              <th scope="col" className="py-1 font-medium">
                Value
              </th>
            </tr>
          </thead>
          <tbody>
            {data.entries.map((e, i) => (
              <tr key={`${e.key}-${i}`} className="border-b border-border last:border-0">
                <th scope="row" className="py-1 pr-3 align-top font-mono font-normal">
                  {e.key}
                </th>
                <td className="py-1 font-mono break-all">{e.value}</td>
              </tr>
            ))}
          </tbody>
        </table>
        <pre
          tabIndex={0}
          aria-label="Raw SSH config"
          className="max-h-[30vh] overflow-auto rounded-lg border border-border bg-background p-2 font-mono text-xs break-words whitespace-pre-wrap"
        >
          {data.raw}
        </pre>
        <p className="text-sm text-muted-foreground">
          Save the text to a file and pass it to <code className="font-mono">ssh -F</code>
          {host ? (
            <>
              , for example <code className="font-mono">ssh -F ./ssh_config {host}</code>.
            </>
          ) : (
            '.'
          )}
        </p>
      </>
    )
  }

  return (
    <>
      <Button
        type="button"
        variant="outline"
        size="sm"
        onClick={() => {
          setCopy('idle')
          setOpen(true)
        }}
      >
        SSH config
      </Button>
      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent className="top-[8%] max-w-2xl">
          <DialogTitle>SSH config for {profile}</DialogTitle>
          <DialogDescription>The settings Colima prints for this profile. They do not update by themselves.</DialogDescription>
          {body}
          <div className="flex flex-wrap items-center gap-2">
            <Button type="button" variant="outline" size="sm" onClick={() => void onCopy()} disabled={!data}>
              Copy
            </Button>
            <span role="status" className="text-sm text-muted-foreground">
              {copy === 'done' && 'Copied.'}
              {copy === 'failed' && 'Could not copy to the clipboard.'}
            </span>
            <Button type="button" size="sm" className="ml-auto" onClick={() => setOpen(false)}>
              Close
            </Button>
          </div>
        </DialogContent>
      </Dialog>
    </>
  )
}
