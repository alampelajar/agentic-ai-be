import { MapPin } from 'lucide-react'
import { words } from './words'

type MapLinkProps = {
  /** The address as people write it. */
  address: string
  label?: string
  className?: string
}

/** Opens the address in the visitor's map app. A plain link: the site itself calls nothing outside. */
export function MapLink({ address, label, className }: MapLinkProps) {
  if (!address.trim()) return null
  return (
    <a
      href={`https://www.google.com/maps/search/?api=1&query=${encodeURIComponent(address)}`}
      target='_blank'
      rel='noreferrer'
      className={className ?? 'inline-flex items-center gap-2 text-primary underline-offset-4 hover:underline'}
    >
      <MapPin className='size-4' aria-hidden='true' />
      {label ?? words.map}
    </a>
  )
}

export default MapLink
