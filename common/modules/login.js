/** @module common/login */

/**
 * A Promise that will be resolved with the users current login status.
 *
 * The resolution object looks like:
 * <pre>
 *   {
 *     "Email": "fred@example.com",
 *     "LoginURL": "https://..."
 *   }
 * </pre>
 *
 * The Email will be the empty string if the user is not logged in.
 */
export var login = fetch('/loginstatus/', {
  credentials: 'include',
}).then(res => {
  if (res.ok) {
    return res.json()
  }
  throw new Error('Problem reading /loginstatus/:' + res.statusText);
});
