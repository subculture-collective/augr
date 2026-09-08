import { EventTimeline } from '@/features/events/EventTimeline'
import { PageHeader } from '@/components/ui/page-header'
import { Breadcrumbs } from '@/shared/components/EntityLinks'
import { useAccount } from '@/shared/account/AccountProvider'

export function EventsPage() {
  const { cockpitPath } = useAccount()
  return (
    <div className="detail-stack">
      <Breadcrumbs items={[{ label: 'Cockpit', to: cockpitPath }, { label: 'Events' }]} />
      <PageHeader eyebrow="Timeline" title="Persisted events" description="Stored agent and pipeline events. Live activity remains in the shell drawer; this page queries persisted evidence." />
      <EventTimeline />
    </div>
  )
}
