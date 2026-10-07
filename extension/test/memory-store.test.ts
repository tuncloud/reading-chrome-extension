import { MemoryStore } from './fakes'
import { storeContract } from './store-contract'

storeContract('MemoryStore', async () => new MemoryStore())
