import { site } from '../site'

type PhotoGalleryProps = {
  /** At most this many photos; all when left out. */
  limit?: number
  className?: string
}

/** The owner's uploaded photos in a grid. Renders nothing when there are none, so no empty frame is ever shown. */
export function PhotoGallery({ limit, className }: PhotoGalleryProps) {
  const photos = limit ? site.images.slice(0, limit) : site.images
  if (photos.length === 0) return null
  return (
    <div className={className ?? 'grid grid-cols-2 gap-3 md:grid-cols-3'}>
      {photos.map((photo) => (
        <img key={photo.src} src={photo.src} alt={photo.alt} loading='lazy' className='aspect-square w-full rounded-md object-cover' />
      ))}
    </div>
  )
}

export default PhotoGallery
