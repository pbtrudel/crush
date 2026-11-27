// memoryStorage is the in-memory Storage the unit tests inject, so a test can
// assert what a module persisted without touching the jsdom localStorage. The
// implementation lives in the app, where a browser that blocks site data uses
// it too.
export { memoryStorage } from '../app/storage'
