package lab.infinitegalaxy.rainclass.data

interface ScreenDataSource {
    suspend fun snapshot(): Snapshot
    suspend fun refresh(): Snapshot
    suspend fun signOut()
}
