// Shared ticking clock so every countdown on the page moves together.
import { ref } from 'vue'

export const nowUnix = ref(Math.floor(Date.now() / 1000))

window.setInterval(() => {
  nowUnix.value = Math.floor(Date.now() / 1000)
}, 30_000)
