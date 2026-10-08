package com.kuaapi.sms.forwarder

import android.content.ContentValues
import android.content.Context
import android.database.sqlite.SQLiteDatabase
import android.database.sqlite.SQLiteOpenHelper

class QueueStore(context: Context) : SQLiteOpenHelper(context, "outbox.db", null, 1) {
    override fun onCreate(db: SQLiteDatabase) {
        db.execSQL("CREATE TABLE outbox (id TEXT PRIMARY KEY, payload BLOB NOT NULL, created INTEGER NOT NULL, blocked INTEGER NOT NULL DEFAULT 0)")
    }
    override fun onUpgrade(db: SQLiteDatabase, oldVersion: Int, newVersion: Int) = Unit
    fun prune(): Int = writableDatabase.delete("outbox", "created < ?", arrayOf((System.currentTimeMillis() - 7 * 86400000L).toString()))
    fun add(id: String, encrypted: ByteArray): Boolean {
        // Return true for duplicate broadcasts without consuming extra queue capacity.
        readableDatabase.rawQuery("SELECT 1 FROM outbox WHERE id=?", arrayOf(id)).use { if (it.moveToFirst()) return true }
        if (counts().first >= 1000) return false
        val row = ContentValues().apply { put("id", id); put("payload", encrypted); put("created", System.currentTimeMillis()) }
        return writableDatabase.insertWithOnConflict("outbox", null, row, SQLiteDatabase.CONFLICT_IGNORE) != -1L
    }
    fun next(): Pair<String, ByteArray>? = readableDatabase.rawQuery("SELECT id,payload FROM outbox WHERE blocked=0 ORDER BY created,id LIMIT 1", null).use {
        if (it.moveToFirst()) it.getString(0) to it.getBlob(1) else null
    }
    fun counts(): Pair<Int, Int> = readableDatabase.rawQuery("SELECT COUNT(*),COALESCE(SUM(blocked),0) FROM outbox", null).use { it.moveToFirst(); it.getInt(0) to it.getInt(1) }
    fun delete(id: String) { writableDatabase.delete("outbox", "id=?", arrayOf(id)) }
    fun block(id: String) { writableDatabase.execSQL("UPDATE outbox SET blocked=1 WHERE id=?", arrayOf(id)) }
    fun unblock() { writableDatabase.execSQL("UPDATE outbox SET blocked=0") }
    fun clear() { writableDatabase.delete("outbox", null, null) }
}
