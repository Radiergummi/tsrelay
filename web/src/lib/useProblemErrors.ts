import { useState } from "react";
import { ApiError, fieldMessage, formMessage } from "@/api/client";
import type { FieldError } from "@/api/schemas";

/**
 * useProblemErrors keeps a form's field errors and its form-level message.
 * capture takes them from a 422, whose field errors go to their fields
 * where shown says the form has a slot for one, and to the form-level
 * message otherwise.
 */
export function useProblemErrors(shown: (field: string) => boolean) {
  const [errors, setErrors] = useState<FieldError[]>([]);
  const [formError, setFormError] = useState("");
  return {
    errors,
    formError,
    /** show replaces the errors, with no form-level message. */
    show: (errs: FieldError[]) => {
      setErrors(errs);
      setFormError("");
    },
    /** fail shows message at the form level, keeping the field errors. */
    fail: setFormError,
    /** capture shows err if it is a 422, and reports whether it was. */
    capture: (err: unknown): boolean => {
      if (!(err instanceof ApiError && err.status === 422)) return false;
      setErrors(err.fieldErrors);
      setFormError(formMessage(err, shown));
      return true;
    },
    message: (field: string) => fieldMessage(errors, field),
  };
}
