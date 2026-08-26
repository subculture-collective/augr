import { LockKeyhole } from 'lucide-react'

import type { UnavailableAutomationJob } from '@/shared/types/domain'

type UnavailableCapabilitiesPanelProps = {
  jobs: UnavailableAutomationJob[]
  singular?: boolean
}

export function UnavailableCapabilitiesPanel({ jobs, singular = false }: UnavailableCapabilitiesPanelProps) {
  const title = singular ? 'Unavailable capability' : 'Unavailable capabilities'
  const description = singular
    ? 'This automation capability is unavailable. The backend reason below explains why it is not registered and cannot be run here.'
    : 'These automation capabilities are unavailable. Backend reasons below explain why they are not registered jobs and cannot be run here.'

  return (
    <section className="panel unavailable-capabilities" aria-labelledby="unavailable-capabilities-title">
      <div className="panel-header unavailable-capabilities-header">
        <div>
          <p className="eyebrow">Capability boundary</p>
          <h2 id="unavailable-capabilities-title">{title}</h2>
          <p className="muted">{description}</p>
        </div>
        <span className="unavailable-count" aria-label={`${jobs.length} unavailable ${jobs.length === 1 ? 'capability' : 'capabilities'}`}>
          <LockKeyhole size={14} aria-hidden="true" />
          {jobs.length} unavailable
        </span>
      </div>
      <ul className="unavailable-capability-list" aria-label="Unavailable automation capabilities">
        {jobs.map((job) => (
          <li key={job.name} className="unavailable-capability-item">
            <span className="unavailable-lock" aria-hidden="true"><LockKeyhole size={17} /></span>
            <div>
              <h3>{job.name}</h3>
              <p><span>Reason:</span> {job.reason}</p>
            </div>
          </li>
        ))}
      </ul>
    </section>
  )
}
