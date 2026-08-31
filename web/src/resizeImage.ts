/** Longest edge of the stored image. Plenty for a thumbnail and a full-screen look. */
const MAX_EDGE = 1024
const JPEG_QUALITY = 0.82

/**
 * Downscales a photo in the browser before it is uploaded.
 *
 * A phone camera photo is 3–5 MB. Sending that over Thai mobile data is the single most
 * likely place this app feels broken, and it buys nothing: 1024px is more than a phone
 * screen shows. Typical result is 100–200 KB.
 */
export async function resizeImage(file: File): Promise<Blob> {
  // from-image so a photo taken in portrait is not stored on its side — phone cameras
  // record orientation in EXIF rather than rotating the pixels.
  const bitmap = await createImageBitmap(file, { imageOrientation: 'from-image' })

  try {
    const scale = Math.min(1, MAX_EDGE / Math.max(bitmap.width, bitmap.height))
    const width = Math.round(bitmap.width * scale)
    const height = Math.round(bitmap.height * scale)

    const canvas = document.createElement('canvas')
    canvas.width = width
    canvas.height = height

    const context = canvas.getContext('2d')
    if (!context) throw new Error('canvas 2d context unavailable')
    context.drawImage(bitmap, 0, 0, width, height)

    const blob = await new Promise<Blob | null>((resolve) =>
      canvas.toBlob(resolve, 'image/jpeg', JPEG_QUALITY),
    )
    if (!blob) throw new Error('could not encode the resized image')
    return blob
  } finally {
    bitmap.close()
  }
}
