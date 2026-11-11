import { createClient, type SupabaseClient, type Session } from '@supabase/supabase-js'

const supabaseUrl = import.meta.env.VITE_SUPABASE_URL || ''
const supabaseAnonKey = import.meta.env.VITE_SUPABASE_ANON_KEY || ''

export const isSupabaseConfigured = Boolean(
  supabaseUrl &&
  supabaseAnonKey &&
  supabaseUrl !== 'https://your-project.supabase.co'
)

export const supabase: SupabaseClient | null = isSupabaseConfigured
  ? createClient(supabaseUrl, supabaseAnonKey, {
      auth: {
        persistSession: true,
        autoRefreshToken: true,
      },
    })
  : null

export async function signInAsGuest(): Promise<{ session: Session | null; error: Error | null }> {
  if (!supabase) {
    return {
      session: null,
      error: new Error('Supabase is not configured yet. Please configure VITE_SUPABASE_URL and VITE_SUPABASE_ANON_KEY in frontend/.env'),
    }
  }

  const { data, error } = await supabase.auth.signInAnonymously()
  if (error) {
    return { session: null, error }
  }
  return { session: data.session, error: null }
}

export async function signInWithEmail(email: string, password: string): Promise<{ session: Session | null; error: Error | null }> {
  if (!supabase) {
    return {
      session: null,
      error: new Error('Supabase is not configured yet.'),
    }
  }

  const { data, error } = await supabase.auth.signInWithPassword({ email, password })
  if (error) {
    return { session: null, error }
  }
  return { session: data.session, error: null }
}

export async function signUpWithEmail(email: string, password: string): Promise<{ session: Session | null; error: Error | null }> {
  if (!supabase) {
    return {
      session: null,
      error: new Error('Supabase is not configured yet.'),
    }
  }

  const { data, error } = await supabase.auth.signUp({ email, password })
  if (error) {
    return { session: null, error }
  }
  return { session: data.session, error: null }
}

export async function signOut(): Promise<void> {
  if (supabase) {
    await supabase.auth.signOut()
  }
}
